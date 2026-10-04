// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/media"
)

const testID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

type libraryStub struct {
	formats      []media.Format
	calls        []string
	actor        achrix.Principal
	target       string
	cursor       string
	limit        int
	revision     int64
	image        []byte
	asset        media.Asset
	page         media.Page
	err          error
	readErr      error
	readAfterErr error
}

func (s *libraryStub) Formats() []media.Format {
	if s.formats != nil {
		return s.formats
	}
	return []media.Format{{MIME: "image/png", Extensions: []string{"png"}}, {MIME: "image/jpeg", Extensions: []string{"jpg", "jpeg", "jpe"}}}
}

func (s *libraryStub) record(op string, p achrix.Principal, target string) {
	s.calls = append(s.calls, op)
	s.actor = p
	s.target = target
}
func (s *libraryStub) Create(_ context.Context, p achrix.Principal, name string, r io.Reader) (media.Asset, error) {
	s.record("create", p, name)
	s.image, _ = io.ReadAll(r)
	return s.asset, s.err
}
func (s *libraryStub) List(_ context.Context, p achrix.Principal, cursor string, limit int) (media.Page, error) {
	s.record("list", p, media.LibraryTarget)
	s.cursor = cursor
	s.limit = limit
	return s.page, s.err
}
func (s *libraryStub) Status(_ context.Context, p achrix.Principal, id string) (media.Asset, error) {
	s.record("status", p, id)
	return s.asset, s.err
}
func (s *libraryStub) Read(_ context.Context, p achrix.Principal, id string, w io.Writer) (media.Asset, error) {
	s.record("read", p, id)
	if s.readErr != nil {
		return media.Asset{}, s.readErr
	}
	_, err := w.Write(s.image)
	if err == nil {
		err = s.readAfterErr
	}
	return s.asset, err
}
func (s *libraryStub) Delete(_ context.Context, p achrix.Principal, id string, revision int64) error {
	s.record("delete", p, id)
	s.revision = revision
	return s.err
}

type transport struct {
	*httptest.ResponseRecorder
	read, write time.Time
	err         error
}

func (w *transport) SetReadDeadline(d time.Time) error  { w.read = d; return w.err }
func (w *transport) SetWriteDeadline(d time.Time) error { w.write = d; return w.err }
func requestView() shell.Request {
	return shell.Request{Principal: "operator", Language: "fa", Allowed: func(cap, target string) bool { return cap == media.List && target == media.LibraryTarget }}
}
func sample() media.Asset {
	return media.Asset{ID: testID, Filename: "تصویر\u200cنمونه.png", MIME: "image/png", Size: 3, Width: 1, Height: 1, Revision: 2, State: "ready"}
}
func handlerFor(s *libraryStub) *handler {
	return &handler{service: s, uploads: make(chan struct{}, 2)}
}
func jsonPost(s *libraryStub, path, body string) *transport {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, r, requestView())
	return w
}
func multipartRequest(t *testing.T, filename string, data []byte, extra string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	switch extra {
	case "field":
		if err = writer.WriteField("other", "ignored"); err != nil {
			t.Fatal(err)
		}
	case "file":
		part, err = writer.CreateFormFile("file", "second.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/upload", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}
func TestUploadFullyChecksFramingBeforeOwningService(t *testing.T) {
	for _, tt := range []struct {
		name, extra string
		data        []byte
		status      int
	}{{"تصویر\u200cنمونه.png", "", []byte("PNG"), 200}, {"one.png", "field", []byte("PNG"), 400}, {"one.png", "file", []byte("PNG"), 400}, {"../one.png", "", []byte("PNG"), 400}, {"one\\other.png", "", []byte("PNG"), 400}, {"empty.png", "", nil, 400}, {"big.png", "", make([]byte, media.MaxUploadBytes+1), 413}} {
		t.Run(tt.name+tt.extra, func(t *testing.T) {
			s := &libraryStub{asset: sample()}
			r := multipartRequest(t, tt.name, tt.data, tt.extra)
			w := &transport{ResponseRecorder: httptest.NewRecorder()}
			handlerFor(s).serve(w, r, requestView())
			if w.Code != tt.status {
				t.Fatal("unexpected upload framing result", w.Code, w.Body.String())
			}
			if tt.status == 200 {
				if len(s.calls) != 1 || s.calls[0] != "create" || s.actor != "operator" || s.target != tt.name || !bytes.Equal(s.image, tt.data) {
					t.Fatal("owning create contract/filename bytes changed")
				}
			} else if len(s.calls) != 0 {
				t.Fatal("invalid framing created durable intent", s.calls)
			}
		})
	}
	s := &libraryStub{asset: sample(), err: achrix.ErrDenied}
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, multipartRequest(t, "denied.png", []byte("PNG"), ""), requestView())
	if w.Code != 403 || len(s.calls) != 1 {
		t.Fatal("owning service denial bypassed", w.Code)
	}
	// Transfer encoding is not silently decoded into a different accepted stream.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	p, err := mw.CreatePart(textproto.MIMEHeader{"Content-Disposition": {mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": "one.png"})}, "Content-Transfer-Encoding": {"quoted-printable"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte("PNG"))
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	s = &libraryStub{}
	w = &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, r, requestView())
	if w.Code != 400 || len(s.calls) != 0 {
		t.Fatal("hidden transfer encoding reached service")
	}
}
func TestUploadAdmissionDeadlineAndUnknownIdentity(t *testing.T) {
	s := &libraryStub{asset: sample()}
	h := handlerFor(s)
	h.uploads <- struct{}{}
	h.uploads <- struct{}{}
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	h.serve(w, multipartRequest(t, "one.png", []byte("PNG"), ""), requestView())
	if w.Code != 429 || len(s.calls) != 0 {
		t.Fatal("upload admission queued excess work")
	}
	r := multipartRequest(t, "one.png", []byte("PNG"), "")
	w = &transport{ResponseRecorder: httptest.NewRecorder(), err: http.ErrNotSupported}
	handlerFor(s).serve(w, r, requestView())
	if w.Code != 503 || len(s.calls) != 0 {
		t.Fatal("unsupported actual read deadline reached create")
	}
	r = multipartRequest(t, "one.png", []byte("PNG"), "")
	ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(time.Second))
	defer cancel()
	r = r.WithContext(ctx)
	w = &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, r, requestView())
	d, _ := ctx.Deadline()
	if !w.read.Equal(d) {
		t.Fatal("upload exceeded earlier caller read deadline")
	}
	s.err = errors.Join(media.ErrUnknownOutcome, context.Canceled)
	w = &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, multipartRequest(t, "one.png", []byte("PNG"), ""), requestView())
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"asset_id":"`+testID+`"`) || !strings.Contains(w.Body.String(), "outcome_unknown") {
		t.Fatal("unknown acknowledged-ID evidence lost", w.Body.String())
	}
}
func TestCollectionMetadataDoesNotGrantBytesOrDelete(t *testing.T) {
	s := &libraryStub{asset: sample(), page: media.Page{Assets: []media.Asset{sample()}, NextCursor: "bounded-cursor"}}
	w := jsonPost(s, "/list", `{"cursor":"first-cursor"}`)
	if w.Code != 200 || s.calls[0] != "list" || s.cursor != "first-cursor" || s.limit != 25 || s.actor != "operator" {
		t.Fatal("bounded owning list contract failed", w.Code, s.calls)
	}
	var got page
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Assets) != 1 || got.Assets[0].Permissions["read"] || got.Assets[0].Permissions["delete"] {
		t.Fatal("list leaked exact-object grants", w.Body.String())
	}
	s.err = achrix.ErrDenied
	w = jsonPost(s, "/status", `{"id":"`+testID+`"}`)
	if w.Code != 403 || s.calls[len(s.calls)-1] != "status" || s.target != testID {
		t.Fatal("status must use exact owning Read authorization")
	}
	w = jsonPost(s, "/delete", `{"id":"`+testID+`","revision":"2"}`)
	if w.Code != 403 || s.calls[len(s.calls)-1] != "delete" {
		t.Fatal("delete bypassed owning permission")
	}
}
func TestDeletePreservesFullInt64RevisionAndRejectsUnexpectedInput(t *testing.T) {
	s := &libraryStub{}
	w := jsonPost(s, "/delete", `{"id":"`+testID+`","revision":"9007199254740993"}`)
	if w.Code != 204 || s.revision != 9007199254740993 {
		t.Fatal("exact CAS revision lost", w.Code, s.revision)
	}
	for _, body := range []string{`{"id":"` + testID + `","revision":9007199254740993}`, `{"id":"` + testID + `","revision":"9223372036854775808"}`, `{"id":"` + testID + `","revision":"2","extra":true}`, `{"id":"` + testID + `","revision":"2"} {}`} {
		before := len(s.calls)
		w = jsonPost(s, "/delete", body)
		if w.Code != 400 || len(s.calls) != before {
			t.Fatal("unsafe CAS/input reached domain", w.Code)
		}
	}
	s.err = media.ErrConflict
	w = jsonPost(s, "/delete", `{"id":"`+testID+`","revision":"2"}`)
	if w.Code != 409 {
		t.Fatal("stale CAS not exposed safely")
	}
}
func TestPrivateAttachmentUsesOwningReadAndDelayedHeaders(t *testing.T) {
	s := &libraryStub{asset: sample(), image: []byte("PNG")}
	r := httptest.NewRequest("GET", "/read/"+testID, nil)
	ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(time.Second))
	defer cancel()
	r = r.WithContext(ctx)
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, r, requestView())
	d, _ := ctx.Deadline()
	if w.Code != 200 || w.Body.String() != "PNG" || strings.Join(s.calls, ",") != "status,read" || s.actor != "operator" || s.target != testID || !w.write.Equal(d) {
		t.Fatal("download did not traverse exact owning contracts/deadline")
	}
	kind, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if err != nil || kind != "attachment" || params["filename"] != s.asset.Filename || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Length") != "3" {
		t.Fatal("private/Unicode attachment protection lost", w.Header())
	}
	for _, failure := range []error{achrix.ErrDenied, media.ErrUnavailable} {
		s.calls = nil
		s.readErr = failure
		w = &transport{ResponseRecorder: httptest.NewRecorder()}
		handlerFor(s).serve(w, r, requestView())
		status := 503
		if errors.Is(failure, achrix.ErrDenied) {
			status = 403
		}
		if w.Code != status || strings.Contains(w.Body.String(), "PNG") || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatal("failed read emitted attachment bytes/headers", w.Code, w.Header())
		}
	}
}
func TestInterruptedAttachmentDoesNotAppendErrorDocument(t *testing.T) {
	s := &libraryStub{asset: sample(), image: []byte("PNG"), readAfterErr: media.ErrUnavailable}
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handlerFor(s).serve(w, httptest.NewRequest("GET", "/read/"+testID, nil), requestView())
	}()
	if recovered != http.ErrAbortHandler || w.Body.String() != "PNG" || strings.Contains(w.Body.String(), "unavailable") {
		t.Fatal("partial attachment must abort without mixing JSON", recovered, w.Body.String())
	}
}

func TestEmptyLocalizedScreenKeepsFormsModuleOwned(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, shell.ErrConfiguration) {
		t.Fatal("nil owning Service accepted")
	}
	for _, language := range []string{"en", "fa"} {
		s := &libraryStub{}
		v := requestView()
		v.Language = language
		w := httptest.NewRecorder()
		v.Render = func(p shell.Page) error {
			if p.Template != libraryTemplate {
				t.Fatal("Media forms left owning Module")
			}
			return p.Template.ExecuteTemplate(w, "content", p.Data)
		}
		handlerFor(s).serve(w, httptest.NewRequest("GET", "/", nil), v)
		if len(s.calls) != 1 || s.limit != 25 || !strings.Contains(w.Body.String(), `id="media-empty"`) || strings.Contains(w.Body.String(), `id="media-file"`) {
			t.Fatal("empty/permitted listing or denied upload presentation failed")
		}
	}
}

func TestScreenUsesEffectiveFormatsAndOmitsUnknownDimensions(t *testing.T) {
	for _, language := range []string{"en", "fa"} {
		for _, formats := range [][]media.Format{
			(&libraryStub{}).Formats(),
			media.SupportedFormats(),
			{{MIME: "application/pdf", Extensions: []string{"pdf"}}},
			{{MIME: "image/svg+xml", Extensions: []string{"svg"}}},
		} {
			a := sample()
			a.MIME, a.Filename, a.Width, a.Height = "application/pdf", "report.pdf", 0, 0
			s := &libraryStub{formats: formats, page: media.Page{Assets: []media.Asset{a}}}
			v := requestView()
			v.Language = language
			v.Allowed = func(cap, _ string) bool { return cap == media.List || cap == media.Create }
			w := httptest.NewRecorder()
			v.Render = func(p shell.Page) error {
				return p.Template.ExecuteTemplate(w, "content", p.Data)
			}
			handlerFor(s).serve(w, httptest.NewRequest("GET", "/", nil), v)
			body := html.UnescapeString(w.Body.String())
			if !strings.Contains(body, `id="media-file"`) || strings.Contains(body, "0×0") || strings.Contains(body, "Image library") || strings.Contains(body, "Ready images") {
				t.Fatal("opaque file screen is inaccurate", body)
			}
			for _, format := range formats {
				if !strings.Contains(body, format.MIME) {
					t.Fatal("effective format absent from rendered browser hint", format.MIME)
				}
				for _, extension := range format.Extensions {
					if !strings.Contains(body, "."+extension) || !strings.Contains(body, strings.ToUpper(extension)) {
						t.Fatal("effective extension absent from accept/help", extension)
					}
				}
			}
			if len(formats) == 1 && (strings.Contains(body, "image/png") || strings.Contains(body, ".png") || strings.Contains(body, "4096")) {
				t.Fatal("narrow upload profile acquired unrelated hints", body)
			}
		}
	}
}

func TestRetainedSupportedAttachmentsIgnoreNarrowedUploadSelection(t *testing.T) {
	for _, format := range media.SupportedFormats() {
		t.Run(format.MIME, func(t *testing.T) {
			a := sample()
			a.MIME, a.Filename, a.Width, a.Height = format.MIME, "original."+format.Extensions[0], 0, 0
			s := &libraryStub{formats: []media.Format{{MIME: "image/png", Extensions: []string{"png"}}}, asset: a, image: []byte("raw")}
			w := &transport{ResponseRecorder: httptest.NewRecorder()}
			handlerFor(s).serve(w, httptest.NewRequest("GET", "/read/"+testID, nil), requestView())
			kind, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if w.Code != 200 || w.Body.String() != "raw" || w.Header().Get("Content-Type") != format.MIME || err != nil || kind != "attachment" || params["filename"] != a.Filename || strings.Join(s.calls, ",") != "status,read" {
				t.Fatal("supported retained bytes were revoked or headers changed", w.Code, w.Header(), s.calls)
			}
		})
	}
	for _, unsupported := range []string{"image/svg+xml; injected=true", "text/html", "application/octet-stream", "image/png; injected=true", "application/pdf\r\nX-Injected: true"} {
		a := sample()
		a.MIME = unsupported
		s := &libraryStub{asset: a, image: []byte("raw")}
		w := &transport{ResponseRecorder: httptest.NewRecorder()}
		handlerFor(s).serve(w, httptest.NewRequest("GET", "/read/"+testID, nil), requestView())
		if w.Code != 503 || strings.Join(s.calls, ",") != "status" || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatal("unknown MIME reached read or attachment headers", unsupported, w.Code, w.Header())
		}
	}
}
