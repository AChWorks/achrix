// SPDX-License-Identifier: MPL-2.0
// Package admin owns Media's private file-library presentation. Every domain
// operation uses the public Media Service, never storage or database internals.
package admin

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/admin/localize"
	"github.com/AChWorks/achrix/media"
)

type library interface {
	Formats() []media.Format
	Create(context.Context, achrix.Principal, string, io.Reader) (media.Asset, error)
	List(context.Context, achrix.Principal, string, int) (media.Page, error)
	Status(context.Context, achrix.Principal, string) (media.Asset, error)
	Read(context.Context, achrix.Principal, string, io.Writer) (media.Asset, error)
	Delete(context.Context, achrix.Principal, string, int64) error
}
type handler struct {
	service library
	uploads chan struct{}
}

// New supplies the real private library screen. Collection metadata entry is
// separate from exact Read/Delete grants; no public URL or maintenance grant.
func New(service *media.Service) (shell.Surface, error) {
	if service == nil {
		return shell.Surface{}, shell.ErrConfiguration
	}
	return surface(service), nil
}
func surface(service library) shell.Surface {
	h := &handler{service: service, uploads: make(chan struct{}, 2)}
	return shell.Surface{ID: "media", Title: shell.Text{Domain: "media", Key: "library.title"}, Catalogs: locales, Capability: media.List, Target: media.LibraryTarget, Handler: h.serve}
}

type asset struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Revision int64  `json:"revision,string"`
	State    string `json:"state"`
}
type item struct {
	Asset       asset           `json:"asset"`
	Permissions map[string]bool `json:"permissions"`
}
type page struct {
	Assets     []item `json:"assets"`
	NextCursor string `json:"next_cursor"`
}
type screen struct {
	Locale     *localize.Localizer
	CanCreate  bool
	Page       page
	Accept     string
	UploadHelp string
}

// The owning public effective inventory supplies browser hints only; Create
// still validates actual bytes. No duplicated upload type allowlist lives here.
func uploadHints(formats []media.Format) (accept, extensions string, decodedImages bool) {
	var choices, names []string
	for _, format := range formats {
		choices = append(choices, format.MIME)
		for _, extension := range format.Extensions {
			choices = append(choices, "."+extension)
			names = append(names, strings.ToUpper(extension))
		}
		decodedImages = decodedImages || format.MIME == "image/png" || format.MIME == "image/jpeg"
	}
	return strings.Join(choices, ","), strings.Join(names, ", "), decodedImages
}

// Admission can narrow after upload. Retained reads use the owning finite
// supported inventory, rather than accidentally revoking previously kept bytes.
var supportedMIMEs = func() map[string]bool {
	result := make(map[string]bool)
	for _, format := range media.SupportedFormats() {
		result[format.MIME] = true
	}
	return result
}()

func present(a media.Asset, view shell.Request) item {
	return item{asset{a.ID, a.Filename, a.MIME, a.Size, a.Width, a.Height, a.Revision, a.State}, map[string]bool{"read": view.Allowed(media.Read, a.ID), "delete": view.Allowed(media.Delete, a.ID)}}
}
func presentPage(p media.Page, view shell.Request) page {
	result := page{Assets: make([]item, 0, len(p.Assets)), NextCursor: p.NextCursor}
	for _, a := range p.Assets {
		result.Assets = append(result.Assets, present(a, view))
	}
	return result
}
func reply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func (h *handler) serve(w http.ResponseWriter, r *http.Request, view shell.Request) {
	if r.Method == http.MethodGet {
		switch {
		case r.URL.Path == "/":
			p, err := h.service.List(r.Context(), view.Principal, "", 25)
			if err != nil {
				fail(w, err, "")
				return
			}
			accept, extensions, decodedImages := uploadHints(h.service.Formats())
			title, err := view.Localizer.Text("media", "library.title", nil)
			if err != nil {
				fail(w, shell.ErrConfiguration, "")
				return
			}
			// Extensions are intrinsically LTR; isolate the inserted value while
			// keeping the whole message plain, autoescaped, reorderable text.
			params := map[string]string{"Extensions": "\u2066" + extensions + "\u2069"}
			helpKey := "upload.help"
			if decodedImages {
				helpKey = "upload.help.images"
				params["MaxDimension"] = strconv.Itoa(media.MaxDimension)
				params["MaxPixels"] = strconv.Itoa(media.MaxPixels)
			}
			help, err := view.Localizer.Text("media", helpKey, params)
			if err != nil {
				fail(w, shell.ErrConfiguration, "")
				return
			}
			if err = view.Render(shell.Page{Title: title, Template: libraryTemplate, Data: screen{Locale: view.Localizer, CanCreate: view.Allowed(media.Create, media.LibraryTarget), Page: presentPage(p, view), Accept: accept, UploadHelp: help}}); err != nil {
				fail(w, shell.ErrConfiguration, "")
			}
		case r.URL.Path == "/media.js":
			data, _ := assets.ReadFile("assets/media.js")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(data)
		case strings.HasPrefix(r.URL.Path, "/read/"):
			h.download(w, r, view, strings.TrimPrefix(r.URL.Path, "/read/"))
		default:
			fail(w, media.ErrNotFound, "")
		}
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var err error
	switch r.URL.Path {
	case "/upload":
		select {
		case h.uploads <- struct{}{}:
			defer func() { <-h.uploads }()
		default:
			fail(w, media.ErrLimited, "")
			return
		}
		var filename string
		var data []byte
		filename, data, err = upload(w, r)
		if err == nil {
			var a media.Asset
			a, err = h.service.Create(r.Context(), view.Principal, filename, bytes.NewReader(data))
			if err == nil {
				reply(w, present(a, view))
				return
			}
			fail(w, err, a.ID)
			return
		}
	case "/list":
		var body struct {
			Cursor string `json:"cursor"`
		}
		if err = decode(w, r, &body); err == nil {
			var p media.Page
			p, err = h.service.List(r.Context(), view.Principal, body.Cursor, 25)
			if err == nil {
				reply(w, presentPage(p, view))
				return
			}
		}
	case "/status":
		var body struct {
			ID string `json:"id"`
		}
		if err = decode(w, r, &body); err == nil {
			var a media.Asset
			a, err = h.service.Status(r.Context(), view.Principal, body.ID)
			if err == nil {
				reply(w, present(a, view))
				return
			}
		}
	case "/delete":
		var body struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision,string"`
		}
		if err = decode(w, r, &body); err == nil {
			err = h.service.Delete(r.Context(), view.Principal, body.ID, body.Revision)
			if err == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	default:
		fail(w, media.ErrNotFound, "")
		return
	}
	fail(w, err, "")
}
func deadline(ctx context.Context, maximum time.Duration) time.Time {
	d := time.Now().Add(maximum)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(d) {
		return earlier
	}
	return d
}

var errTooLarge = errors.New("media admin input too large")

// Fully validate framing before durable Media intent. Two fixed 10MiB+1 buffers
// bound file preflight; no temporary files or multipart disk spill are created.
func upload(w http.ResponseWriter, r *http.Request) (string, []byte, error) {
	if r.ContentLength > 12<<20 {
		return "", nil, errTooLarge
	}
	if len(r.Header.Values("Content-Type")) != 1 {
		return "", nil, media.ErrInput
	}
	kind, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/form-data" || len(params) != 1 || params["boundary"] == "" || len(params["boundary"]) > 70 {
		return "", nil, media.ErrInput
	}
	if http.NewResponseController(w).SetReadDeadline(deadline(r.Context(), 8*time.Second)) != nil {
		return "", nil, media.ErrUnavailable
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	defer r.Body.Close()
	reader, err := r.MultipartReader()
	if err != nil {
		return "", nil, media.ErrInput
	}
	part, err := reader.NextRawPart()
	if err != nil {
		return "", nil, inputError(err)
	}
	disposition, fields, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil || disposition != "form-data" || len(fields) != 2 || fields["name"] != "file" || fields["filename"] == "" || fields["filename"] != part.FileName() || strings.ContainsAny(fields["filename"], "/\\") {
		return "", nil, media.ErrInput
	}
	// No hidden transfer decoding or additional attacker-specified part headers.
	for name, values := range part.Header {
		if len(values) != 1 || (name != "Content-Disposition" && name != "Content-Type") {
			return "", nil, media.ErrInput
		}
	}
	data := make([]byte, media.MaxUploadBytes+1)
	n, err := io.ReadFull(part, data)
	if int64(n) > media.MaxUploadBytes {
		return "", nil, errTooLarge
	}
	if err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", nil, inputError(err)
	}
	if n == 0 {
		return "", nil, media.ErrInput
	}
	// NextRawPart must see the terminal boundary, never another file or field.
	if _, err = reader.NextRawPart(); err != io.EOF {
		if err != nil {
			return "", nil, inputError(err)
		}
		return "", nil, media.ErrInput
	}
	return fields["filename"], data[:n], nil
}
func inputError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errTooLarge
	}
	var timedOut interface{ Timeout() bool }
	if errors.As(err, &timedOut) && timedOut.Timeout() {
		return media.ErrUnavailable
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return media.ErrUnavailable
	}
	return media.ErrInput
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" {
		return media.ErrInput
	}
	if http.NewResponseController(w).SetReadDeadline(deadline(r.Context(), 2*time.Second)) != nil {
		return media.ErrUnavailable
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return inputError(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return media.ErrInput
	}
	return nil
}

// Download headers are delayed until Media has verified the file and writes its
// first byte. Errors before bytes retain a safe JSON response, never fake MIME.
type attachmentWriter struct {
	writer  http.ResponseWriter
	asset   media.Asset
	started bool
}

func (w *attachmentWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.writer.Header().Set("Content-Type", w.asset.MIME)
		w.writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": w.asset.Filename}))
		w.writer.Header().Set("Content-Length", strconv.FormatInt(w.asset.Size, 10))
		w.writer.Header().Set("Cache-Control", "private, no-store")
		w.writer.Header().Set("X-Content-Type-Options", "nosniff")
		w.started = true
	}
	return w.writer.Write(data)
}
func (h *handler) download(w http.ResponseWriter, r *http.Request, view shell.Request, id string) {
	if http.NewResponseController(w).SetWriteDeadline(deadline(r.Context(), 12*time.Second)) != nil {
		fail(w, media.ErrUnavailable, "")
		return
	}
	a, err := h.service.Status(r.Context(), view.Principal, id)
	if err != nil {
		fail(w, err, "")
		return
	}
	if a.State != "ready" {
		fail(w, media.ErrConflict, "")
		return
	}
	if !supportedMIMEs[a.MIME] || a.Size < 1 || a.Size > media.MaxUploadBytes {
		fail(w, media.ErrUnavailable, "")
		return
	}
	output := &attachmentWriter{writer: w, asset: a}
	_, err = h.service.Read(r.Context(), view.Principal, id, output)
	if err == nil && !output.started {
		err = media.ErrUnavailable
	}
	if err != nil {
		if output.started {
			panic(http.ErrAbortHandler)
		}
		fail(w, err, "")
	}
}
func fail(w http.ResponseWriter, err error, id string) {
	status, code := 503, "unavailable"
	switch {
	case errors.Is(err, media.ErrUnknownOutcome):
		code = "outcome_unknown"
	case errors.Is(err, achrix.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, errTooLarge):
		status, code = 413, "input_too_large"
	case errors.Is(err, media.ErrInput):
		status, code = 400, "invalid_input"
	case errors.Is(err, media.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, media.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, media.ErrLimited):
		status, code = 429, "busy"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		AssetID string `json:"asset_id,omitempty"`
	}{code, id})
}

//go:embed assets/*
var assets embed.FS

// Each presentation owns its catalogs; the shell composes these at startup.
//
//go:embed locales/*.json
var locales embed.FS
var libraryTemplate = template.Must(template.New("library").Parse(`{{define "content"}}
<script src="/admin/media/media.js" defer></script>
<p>{{.Locale.Text "media" "library.help" nil}}</p>
{{if .CanCreate}}<section aria-labelledby="upload-heading"><h2 id="upload-heading">{{.Locale.Text "media" "upload.title" nil}}</h2><form data-admin-form data-multipart data-operation="upload" action="/admin/media/upload" method="post" enctype="multipart/form-data"><label for="media-file">{{.Locale.Text "media" "upload.file" nil}}</label><input id="media-file" name="file" type="file" accept="{{.Accept}}" required aria-describedby="upload-help"><p id="upload-help">{{.UploadHelp}}</p><button type="submit" disabled>{{.Locale.Text "media" "upload.action" nil}}</button></form><p id="upload-result" role="status"></p></section>{{end}}
<section aria-labelledby="library-heading"><h2 id="library-heading">{{.Locale.Text "media" "library.ready" nil}}</h2><form id="media-refresh" data-admin-form data-read-only data-operation="list" action="/admin/media/list" method="post"><input name="cursor" type="hidden" value=""><button type="submit" disabled>{{.Locale.Text "media" "library.refresh" nil}}</button></form>
<form id="media-next-page" data-admin-form data-read-only data-operation="list" action="/admin/media/list" method="post" {{if not .Page.NextCursor}}hidden{{end}}><input id="media-next-cursor" name="cursor" type="hidden" value="{{.Page.NextCursor}}"><button type="submit" disabled>{{.Locale.Text "media" "library.next" nil}}</button></form>
<p id="media-empty" {{if .Page.Assets}}hidden{{end}}>{{.Locale.Text "media" "library.empty" nil}}</p><table id="media-table" {{if not .Page.Assets}}hidden{{end}}><caption>{{.Locale.Text "media" "library.page_help" nil}}</caption><thead><tr><th scope="col">{{.Locale.Text "media" "asset.name_id" nil}}</th><th scope="col">{{.Locale.Text "media" "asset.details" nil}}</th><th scope="col">{{.Locale.Text "media" "asset.actions" nil}}</th></tr></thead><tbody id="assets">{{range .Page.Assets}}<tr data-asset-id="{{.Asset.ID}}"><td><bdi dir="auto">{{.Asset.Filename}}</bdi><br><bdi dir="ltr">{{.Asset.ID}}</bdi></td><td><bdi dir="ltr">{{.Asset.MIME}} · {{.Asset.Size}} B{{if and (gt .Asset.Width 0) (gt .Asset.Height 0)}} · {{.Asset.Width}}×{{.Asset.Height}}{{end}}</bdi></td><td>{{if .Permissions.read}}<a data-download href="/admin/media/read/{{.Asset.ID}}">{{$.Locale.Text "media" "download.action" nil}}</a>{{end}}{{if .Permissions.delete}}<form data-admin-form data-operation="delete" action="/admin/media/delete" method="post"><input name="id" type="hidden" value="{{.Asset.ID}}"><input name="revision" type="hidden" value="{{.Asset.Revision}}"><label><input type="checkbox" data-confirm required>{{$.Locale.Text "media" "delete.confirm" nil}}</label><button type="submit" disabled>{{$.Locale.Text "media" "delete.action" nil}}</button></form>{{end}}{{if and (not .Permissions.read) (not .Permissions.delete)}}<p>{{$.Locale.Text "media" "asset.denied" nil}}</p>{{end}}</td></tr>{{end}}</tbody></table></section>
<section aria-labelledby="asset-status-heading"><h2 id="asset-status-heading">{{.Locale.Text "media" "status.title" nil}}</h2><form data-admin-form data-read-only data-operation="status" action="/admin/media/status" method="post"><label for="media-status-id">{{.Locale.Text "media" "asset.id" nil}}</label><input id="media-status-id" name="id" dir="ltr" maxlength="26" minlength="26" pattern="[A-Z2-7]{26}" required autocomplete="off"><button type="submit" disabled>{{.Locale.Text "media" "status.action" nil}}</button></form><p id="asset-status" role="status"></p><p>{{.Locale.Text "media" "status.help" nil}}</p></section>
{{end}}`))
