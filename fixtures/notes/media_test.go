// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	"github.com/AChWorks/achrix/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mediaFixtureLogin = "media-consumer"
const mediaFixturePassword = "Synthetic Media consumer password 123!"
const mediaFixtureBootstrap achrix.Principal = "media-consumer-bootstrap"
const retainedPNGName = "تصویر\u200cنمونه.png"
const retainedJPEGName = "نمونه.jpg"

// This product policy owns grants, never Media. Collection discovery does not
// grant byte access or deletion, and upload does not infer object ownership.
type mediaConsumerPolicy struct {
	account atomic.Value
	create  atomic.Bool
	list    atomic.Bool
	cleanup atomic.Bool
	mu      sync.Mutex
	read    map[string]bool
	delete  map[string]bool
}

func newMediaConsumerPolicy() *mediaConsumerPolicy {
	p := &mediaConsumerPolicy{read: make(map[string]bool), delete: make(map[string]bool)}
	p.account.Store("")
	return p
}
func (p *mediaConsumerPolicy) Authorize(_ context.Context, actor achrix.Principal, capability, target string) error {
	if actor == identity.PublicPrincipal && capability == identity.Authentication && target == "" {
		return nil
	}
	if actor == mediaFixtureBootstrap && (capability == identity.AccountCreate || capability == audit.Append) {
		return nil
	}
	if id := p.account.Load().(string); id == "" || actor != achrix.Principal(id) {
		return achrix.ErrDenied
	}
	if target == media.LibraryTarget {
		switch capability {
		case media.Create:
			if p.create.Load() {
				return nil
			}
		case media.List:
			if p.list.Load() {
				return nil
			}
		case media.Reconcile:
			if p.cleanup.Load() {
				return nil
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if capability == media.Read && p.read[target] || capability == media.Delete && p.delete[target] {
		return nil
	}
	return achrix.ErrDenied
}
func (p *mediaConsumerPolicy) grant(id string, deletion bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.read[id] = true
	if deletion {
		p.delete[id] = true
	}
}

type mediaConsumer struct {
	app      *achrix.Application
	service  *media.Service
	identity *identity.Service
	policy   *mediaConsumerPolicy
	pool     *pgxpool.Pool
	root     string
}

// Destructive setup is confined to one fixed, explicitly provided disposable DB
// and an existing private storage root. Restore verifies ledgers without reset.
func composeMediaConsumer(t *testing.T, restore bool) *mediaConsumer {
	t.Helper()
	dsn, root := os.Getenv("NOTES_MEDIA_DATABASE_URL"), os.Getenv("NOTES_MEDIA_STORAGE_ROOT")
	c, err := pgxpool.ParseConfig(dsn)
	want := "achrix_media_consumer"
	if restore {
		want = "achrix_media_restore"
	}
	if err != nil || dsn == "" || c.ConnConfig.Database != want || !filepath.IsAbs(root) {
		t.Fatal("explicit private Media consumer database and storage required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("Media consumer storage must be an existing private directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.MaxConns = 2
	c.ConnConfig.ConnectTimeout = time.Second
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal("private Media consumer database unavailable")
	}
	t.Cleanup(p.Close)
	var database string
	var version int
	var encoding string
	if err := p.QueryRow(ctx, "SELECT current_database(),current_setting('server_version_num')::integer,current_setting('server_encoding')").Scan(&database, &version, &encoding); err != nil || database != want || version < 180000 || version >= 190000 || encoding != "UTF8" {
		t.Fatal("Media consumer database identity/profile mismatch")
	}
	if !restore {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("source Media proof requires empty task-owned storage")
		}
		if _, err := p.Exec(ctx, "DROP SCHEMA IF EXISTS media CASCADE; DROP SCHEMA IF EXISTS identity CASCADE; DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
			t.Fatal("private Media consumer schema reset failed")
		}
	}
	for _, migrate := range []func(context.Context, string) error{audit.Migrate, identity.Migrate, media.Migrate} {
		if err := migrate(ctx, dsn); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	auditModule, err := audit.NewPostgres(dsn, audit.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	identityModule, err := identity.NewPostgres(dsn, identity.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	mediaModule, err := media.NewPostgres(dsn, media.Config{StorageRoot: root, AllowedMIMEs: []string{"image/png", "image/jpeg", "application/pdf", "application/zip"}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := newMediaConsumerPolicy()
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second, Logger: logger}, policy, mediaModule, identityModule, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	accountability, err := audit.NewService(app, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := identity.NewService(app, identityModule, accountability)
	if err != nil {
		t.Fatal(err)
	}
	service, err := media.NewService(app, mediaModule)
	if err != nil {
		t.Fatal(err)
	}
	return &mediaConsumer{app, service, identities, policy, p, root}
}

func mediaImageBytes(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 31, 17))
	for y := 0; y < 17; y++ {
		for x := 0; x < 31; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 7), uint8(y * 11), 150, 255})
		}
	}
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Complete synthetic files shared by the independent consumer and browser
// fixture. No third-party document or opaque signature stub is used.
func mediaDocumentBytes(t testing.TB) map[string][]byte {
	t.Helper()
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 100] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	content := "BT /F1 12 Tf 20 50 Td (AChrix owned media fixture) Tj ET\n"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	var bundle bytes.Buffer
	archive := zip.NewWriter(&bundle)
	file, err := archive.CreateHeader(&zip.FileHeader{Name: "fixture.txt", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("AChrix owned media fixture\n")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"document.pdf": pdf.Bytes(), "bundle.zip": bundle.Bytes()}
}

// Delay attachment metadata until the public service has verified stored
// integrity and writes its first byte. Failure before bytes keeps error headers.
type mediaConsumerDownloadWriter struct {
	response http.ResponseWriter
	asset    media.Asset
	started  bool
}

func (w *mediaConsumerDownloadWriter) Write(body []byte) (int, error) {
	if !w.started {
		w.response.Header().Set("Content-Type", w.asset.MIME)
		w.response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": w.asset.Filename}))
		w.response.Header().Set("Content-Length", strconv.FormatInt(w.asset.Size, 10))
		w.started = true
	}
	return w.response.Write(body)
}

// This test-only product ingress uses the existing Identity cookie/CSRF boundary.
// TLS and actual read deadlines bound untrusted network readers; Media's trusted
// synchronous io.Reader/io.Writer contracts do not interrupt arbitrary callbacks.
func mediaConsumerHandler(web *identity.Web, service *media.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/identity/", http.StripPrefix("/identity", web.Handler()))
	mux.HandleFunc("/media/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		mutation := r.Method == http.MethodPost || r.Method == http.MethodDelete
		actor, err := web.AuthenticateRequest(r, mutation)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/media/upload" {
			if len(r.Header.Values("X-Media-Filename")) != 1 || len(r.Header.Get("X-Media-Filename")) > 256 || r.Header.Get("Content-Type") != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			controller := http.NewResponseController(w)
			if err := controller.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
			r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes+1)
			defer r.Body.Close()
			asset, err := service.Create(ctx, actor, r.Header.Get("X-Media-Filename"), r.Body)
			if err != nil {
				mediaHTTPError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(asset)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/media/list" {
			page, err := service.List(ctx, actor, "", 1)
			if err != nil {
				mediaHTTPError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/media/")
		if r.Method == http.MethodGet {
			asset, err := service.Status(ctx, actor, id)
			if err != nil {
				mediaHTTPError(w, err)
				return
			}
			destination := &mediaConsumerDownloadWriter{response: w, asset: asset}
			if _, err := service.Read(ctx, actor, id, destination); err != nil && !destination.started {
				mediaHTTPError(w, err)
			}
			return
		}
		if r.Method == http.MethodDelete {
			revision, err := strconv.ParseInt(r.Header.Get("If-Match"), 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := service.Delete(ctx, actor, id, revision); err != nil {
				mediaHTTPError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	return mux
}
func mediaHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, achrix.ErrDenied):
		status = http.StatusForbidden
	case errors.Is(err, media.ErrInput):
		status = http.StatusBadRequest
	case errors.Is(err, media.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, media.ErrConflict):
		status = http.StatusConflict
	}
	w.WriteHeader(status)
}
func mediaHTTPSRequest(t *testing.T, server *httptest.Server, method, path, origin, csrf, filename string, content []byte, cookie *http.Cookie, revision int64) identityHTTPResult {
	t.Helper()
	r, err := http.NewRequest(method, server.URL+path, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/octet-stream")
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if filename != "" {
		r.Header.Set("X-Media-Filename", filename)
	}
	if revision > 0 {
		r.Header.Set("If-Match", strconv.FormatInt(revision, 10))
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	client := server.Client()
	client.Timeout = 6 * time.Second
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("private Media fixture HTTPS request failed")
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(b) > 16384 {
		t.Fatal("private Media response bound failed")
	}
	return identityHTTPResult{response.StatusCode, response.Header.Clone(), response.Cookies(), b}
}
func mediaSnapshot(t *testing.T, f *mediaConsumer) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var assets, accounts, events, sessions int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM media.assets),(SELECT count(*) FROM identity.accounts),(SELECT count(*) FROM audit.records),(SELECT count(*) FROM identity.sessions)").Scan(&assets, &accounts, &events, &sessions); err != nil {
		t.Fatal(err)
	}
	files := mediaFiles(t, f.root)
	b, err := json.Marshal(struct {
		Assets, Accounts, Events, Sessions int
		Files                              map[string]string
	}{assets, accounts, events, sessions, files})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func mediaFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("private Media storage contains symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("private Media storage has nonregular file")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		files[relative] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
func mediaDeadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
func mediaCheckAsset(t *testing.T, a media.Asset, name, format string, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	mimeType, width, height := format, 0, 0
	if format == "png" || format == "jpeg" {
		mimeType, width, height = "image/"+format, 31, 17
	}
	if a.ID == "" || len(a.ID) != 26 || a.Filename != name || a.MIME != mimeType || a.Size != int64(len(body)) || a.Width != width || a.Height != height || a.SHA256 != hex.EncodeToString(sum[:]) || a.Revision < 1 || a.State != "ready" || a.CreatedAt.IsZero() || a.CreatedAt.Location() != time.UTC {
		t.Fatalf("public Media metadata differs: ID=%q MIME=%q Size=%d dimensions=%dx%d Revision=%d State=%q CreatedAt=%s location=%s", a.ID, a.MIME, a.Size, a.Width, a.Height, a.Revision, a.State, a.CreatedAt.Format(time.RFC3339Nano), a.CreatedAt.Location())
	}
}

type mediaBoundedWriter struct {
	bytes.Buffer
	largest int
}

func (w *mediaBoundedWriter) Write(b []byte) (int, error) {
	if len(b) > 32<<10 {
		return 0, errors.New("consumer writer chunk limit exceeded")
	}
	w.largest = max(w.largest, len(b))
	return w.Buffer.Write(b)
}

func TestMediaPublicConsumer(t *testing.T) {
	if os.Getenv("NOTES_MEDIA_RESTORE_VERIFY") == "1" {
		t.Skip("source test does not reset restored data")
	}
	f := composeMediaConsumer(t, false)
	ctx, cancel := mediaDeadline()
	defer cancel()
	account, err := f.identity.CreateAccount(ctx, mediaFixtureBootstrap, mediaFixtureLogin, mediaFixturePassword)
	if err != nil || account.ID == "" {
		t.Fatal("Media consumer account provisioning failed")
	}
	f.policy.account.Store(account.ID)
	actor := achrix.Principal(account.ID)
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	web, err := identity.NewWeb(f.identity, origin)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = mediaConsumerHandler(web, f.service)
	server.Config.ReadHeaderTimeout = time.Second
	server.Config.ReadTimeout = 5 * time.Second
	server.Config.WriteTimeout = 5 * time.Second
	server.Config.IdleTimeout = 30 * time.Second
	server.Config.MaxHeaderBytes = 8 << 10
	server.StartTLS()
	t.Cleanup(server.Close)
	loginBody, _ := json.Marshal(map[string]string{"login": mediaFixtureLogin, "password": mediaFixturePassword})
	cookie, csrf := fixtureSession(t, identityRequest(t, server, "POST", "/identity/login", origin, "", string(loginBody), nil), actor)
	pngBody, jpegBody := mediaImageBytes(t, "png"), mediaImageBytes(t, "jpeg")
	before := mediaSnapshot(t, f)
	if _, err := f.service.Create(ctx, identity.PublicPrincipal, retainedPNGName, bytes.NewReader(pngBody)); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("public authentication-attempt principal acquired Media authority")
	}
	if _, err := f.service.Create(ctx, actor, retainedPNGName, bytes.NewReader(pngBody)); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("authentication implicitly authorized Media creation")
	}
	if got := mediaHTTPSRequest(t, server, "POST", "/media/upload", origin, csrf, retainedPNGName, pngBody, cookie, 0); got.status != http.StatusForbidden {
		t.Fatal("HTTPS authentication bypassed separate Media authorization", got.status)
	}
	if mediaSnapshot(t, f) != before {
		t.Fatal("denied Media create had side effects")
	}
	f.policy.create.Store(true)
	for _, probe := range []struct {
		origin, csrf string
		cookie       *http.Cookie
	}{
		{"https://attacker.invalid", csrf, cookie}, {origin, "wrong-csrf", cookie}, {origin, csrf, nil},
	} {
		if got := mediaHTTPSRequest(t, server, "POST", "/media/upload", probe.origin, probe.csrf, retainedPNGName, pngBody, probe.cookie, 0); got.status != http.StatusUnauthorized {
			t.Fatal("Media ingress accepted origin/CSRF/anonymous probe", got.status)
		}
	}
	if mediaSnapshot(t, f) != before {
		t.Fatal("rejected Media ingress had side effects")
	}
	created := mediaHTTPSRequest(t, server, "POST", "/media/upload", origin, csrf, retainedPNGName, pngBody, cookie, 0)
	var pngAsset media.Asset
	if created.status != http.StatusCreated || json.Unmarshal(created.body, &pngAsset) != nil {
		t.Fatal("authorized HTTPS image upload failed", created.status)
	}
	mediaCheckAsset(t, pngAsset, retainedPNGName, "png", pngBody)
	before = mediaSnapshot(t, f)
	if got := mediaHTTPSRequest(t, server, "GET", "/media/"+pngAsset.ID, origin, "", "", nil, cookie, 0); got.status != http.StatusForbidden {
		t.Fatal("uploader implicitly acquired object read permission", got.status)
	}
	if err := f.service.Delete(ctx, actor, pngAsset.ID, pngAsset.Revision); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("upload implicitly authorized delete")
	}
	if _, err := f.service.List(ctx, actor, "", 1); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("upload implicitly authorized collection discovery")
	}
	f.policy.list.Store(true)
	if _, err := f.service.Status(ctx, actor, pngAsset.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("collection permission authorized exact object metadata")
	}
	if mediaSnapshot(t, f) != before {
		t.Fatal("denied Media read/list/delete had side effects")
	}
	f.policy.grant(pngAsset.ID, false)
	read := mediaHTTPSRequest(t, server, "GET", "/media/"+pngAsset.ID, origin, "", "", nil, cookie, 0)
	disposition, parameters, err := mime.ParseMediaType(read.headers.Get("Content-Disposition"))
	if read.status != http.StatusOK || !bytes.Equal(read.body, pngBody) || read.headers.Get("Content-Type") != "image/png" || read.headers.Get("X-Content-Type-Options") != "nosniff" || read.headers.Get("Cache-Control") != "private, no-store" || err != nil || disposition != "attachment" || parameters["filename"] != retainedPNGName {
		t.Fatal("private authenticated byte serving contract differs")
	}
	jpegAsset, err := f.service.Create(ctx, actor, retainedJPEGName, bytes.NewReader(jpegBody))
	if err != nil {
		t.Fatal("public JPEG upload failed", err)
	}
	mediaCheckAsset(t, jpegAsset, retainedJPEGName, "jpeg", jpegBody)
	f.policy.grant(jpegAsset.ID, false)
	retained := []media.Asset{pngAsset, jpegAsset}
	for _, name := range []string{"document.pdf", "bundle.zip"} {
		body := mediaDocumentBytes(t)[name]
		created := mediaHTTPSRequest(t, server, "POST", "/media/upload", origin, csrf, name, body, cookie, 0)
		var asset media.Asset
		if created.status != http.StatusCreated || json.Unmarshal(created.body, &asset) != nil {
			t.Fatal("opaque HTTPS upload", name, created.status)
		}
		mimeType := "application/pdf"
		if name == "bundle.zip" {
			mimeType = "application/zip"
		}
		mediaCheckAsset(t, asset, name, mimeType, body)
		if got := mediaHTTPSRequest(t, server, "GET", "/media/"+asset.ID, origin, "", "", nil, cookie, 0); got.status != http.StatusForbidden || len(got.body) != 0 {
			t.Fatal("opaque upload widened byte permission")
		}
		f.policy.grant(asset.ID, false)
		status, err := f.service.Status(ctx, actor, asset.ID)
		if err != nil || status != asset {
			t.Fatal("opaque public status", err)
		}
		read := mediaHTTPSRequest(t, server, "GET", "/media/"+asset.ID, origin, "", "", nil, cookie, 0)
		disposition, parameters, err := mime.ParseMediaType(read.headers.Get("Content-Disposition"))
		if read.status != http.StatusOK || !bytes.Equal(read.body, body) || read.headers.Get("Content-Type") != mimeType || read.headers.Get("X-Content-Type-Options") != "nosniff" || read.headers.Get("Cache-Control") != "private, no-store" || err != nil || disposition != "attachment" || parameters["filename"] != name {
			t.Fatal("opaque private attachment headers/original bytes", name)
		}
		retained = append(retained, asset)
	}
	// Public immutable-ID keyset traversal is bounded, distinct and terminates.
	seen := make(map[string]bool)
	cursor := ""
	for i := 0; i < 5; i++ {
		page, err := f.service.List(ctx, actor, cursor, 1)
		if err != nil || len(page.Assets) != 1 || seen[page.Assets[0].ID] {
			t.Fatal("bounded Media list contract failed")
		}
		seen[page.Assets[0].ID] = true
		if page.NextCursor == "" {
			break
		}
		if cursor == page.NextCursor {
			t.Fatal("Media list cursor failed to progress")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 4 || !seen[pngAsset.ID] || !seen[jpegAsset.ID] {
		t.Fatal("retained Media listing differs")
	}
	for _, limit := range []int{0, 101} {
		if _, err := f.service.List(ctx, actor, "", limit); !errors.Is(err, media.ErrInput) {
			t.Fatal("unbounded Media list accepted")
		}
	}
	if _, err := f.service.List(ctx, actor, "untrusted-cursor", 1); !errors.Is(err, media.ErrInput) {
		t.Fatal("invalid list cursor accepted")
	}
	for _, name := range []string{"../escape.png", "folder/image.png", "back\\slash.png", "control\x00.png", "direction\u202e.png"} {
		if _, err := f.service.Create(ctx, actor, name, bytes.NewReader(pngBody)); !errors.Is(err, media.ErrInput) {
			t.Fatal("unsafe filename accepted")
		}
	}
	for _, invalid := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), []byte("<html>active input</html>"), []byte("GIF89a"), pngBody[:12]} {
		if _, err := f.service.Create(ctx, actor, "claimed.png", bytes.NewReader(invalid)); !errors.Is(err, media.ErrInput) {
			t.Fatal("unsupported/truncated image accepted", err)
		}
	}
	temporary, err := f.service.Create(ctx, actor, "temporary.png", bytes.NewReader(pngBody))
	if err != nil {
		t.Fatal(err)
	}
	f.policy.grant(temporary.ID, true)
	// Stored integrity failure sends no attachment metadata or original bytes.
	if err := os.WriteFile(filepath.Join(f.root, temporary.ID), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	failedRead := mediaHTTPSRequest(t, server, "GET", "/media/"+temporary.ID, origin, "", "", nil, cookie, 0)
	if failedRead.status != http.StatusServiceUnavailable || len(failedRead.body) != 0 || failedRead.headers.Get("Content-Disposition") != "" || failedRead.headers.Get("Content-Type") == "image/png" {
		t.Fatal("integrity failure exposed attachment headers/bytes")
	}
	if err := os.WriteFile(filepath.Join(f.root, temporary.ID), pngBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, actor, temporary.ID, temporary.Revision+1); !errors.Is(err, media.ErrConflict) {
		t.Fatal("stale delete precondition accepted", err)
	}
	status, err := f.service.Status(ctx, actor, temporary.ID)
	if err != nil || status.State != "ready" || status.Revision != temporary.Revision {
		t.Fatal("conflicting delete changed durable state")
	}
	if got := mediaHTTPSRequest(t, server, "DELETE", "/media/"+temporary.ID, origin, csrf, "", nil, cookie, temporary.Revision); got.status != http.StatusNoContent {
		t.Fatal("authorized conditional HTTPS delete failed", got.status)
	}
	status, err = f.service.Status(ctx, actor, temporary.ID)
	if err != nil || status.State != "deleted" || status.Filename != "" || status.Size != 0 || status.SHA256 != "" {
		t.Fatal("deleted asset tombstone exposes retained content metadata")
	}
	var deleted bytes.Buffer
	if _, err := f.service.Read(ctx, actor, temporary.ID, &deleted); !errors.Is(err, media.ErrNotFound) || deleted.Len() != 0 {
		t.Fatal("deleted asset remained readable", err)
	}
	unknownID := rand.Text()
	if _, err := f.service.Status(ctx, actor, unknownID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("unknown object bypassed exact authorization")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	var canceledBytes bytes.Buffer
	if _, err := f.service.Read(canceled, actor, pngAsset.ID, &canceledBytes); !errors.Is(err, context.Canceled) || canceledBytes.Len() != 0 {
		t.Fatal("canceled read produced bytes", err)
	}
	for _, a := range retained {
		w := &mediaBoundedWriter{}
		found, err := f.service.Read(ctx, actor, a.ID, w)
		if err != nil || found.ID != a.ID || w.Len() != int(a.Size) || w.largest > 32<<10 {
			t.Fatal("public bounded-writer image read failed", err)
		}
	}
	if _, err := f.service.Reconcile(ctx, actor, 1); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("collection cleanup granted implicitly")
	}
	f.policy.cleanup.Store(true)
	if _, err := f.service.Reconcile(ctx, actor, 40); err != nil {
		t.Fatal("authorized bounded explicit reconciliation failed", err)
	}
	page, err := f.service.List(ctx, actor, "", 100)
	if err != nil || len(page.Assets) != 4 {
		t.Fatal("failed upload/delete polluted retained ready collection")
	}
	// Close ingress first, then stop all participating Modules before native
	// PostgreSQL+filesystem capture by validate.sh. No producer can race capture.
	server.Close()
	if err := f.app.Shutdown(context.Background()); err != nil {
		t.Fatal("Media capture quiescence failed", err)
	}
	if _, err := f.service.List(ctx, actor, "", 1); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal("stopped composition accepted Media work", err)
	}
}

func TestMediaTrustedRestore(t *testing.T) {
	if os.Getenv("NOTES_MEDIA_RESTORE_VERIFY") != "1" {
		t.Skip("validate.sh runs this explicitly after coherent private restore")
	}
	f := composeMediaConsumer(t, true)
	ctx, cancel := mediaDeadline()
	defer cancel()
	session, err := f.identity.Login(ctx, mediaFixtureLogin, mediaFixturePassword, "")
	if err != nil || session.Principal == "" {
		t.Fatal("restored Identity credentials cannot authenticate")
	}
	f.policy.account.Store(string(session.Principal))
	f.policy.list.Store(true)
	page, err := f.service.List(ctx, session.Principal, "", 100)
	if err != nil || len(page.Assets) != 4 || page.NextCursor != "" {
		t.Fatal("coherently restored Media collection differs")
	}
	bodies := mediaDocumentBytes(t)
	bodies[retainedPNGName] = mediaImageBytes(t, "png")
	bodies[retainedJPEGName] = mediaImageBytes(t, "jpeg")
	for _, asset := range page.Assets {
		body, ok := bodies[asset.Filename]
		if !ok {
			t.Fatal("unexpected retained asset")
		}
		format := map[string]string{retainedPNGName: "png", retainedJPEGName: "jpeg", "document.pdf": "application/pdf", "bundle.zip": "application/zip"}[asset.Filename]
		mediaCheckAsset(t, asset, asset.Filename, format, body)
		if _, err := f.service.Status(ctx, session.Principal, asset.ID); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal("restored collection discovery widened byte rights")
		}
		f.policy.grant(asset.ID, false)
		status, err := f.service.Status(ctx, session.Principal, asset.ID)
		if err != nil || status.ID != asset.ID || status.SHA256 != asset.SHA256 || status.Revision != asset.Revision {
			t.Fatal("restored public asset identity differs")
		}
		var restored bytes.Buffer
		if _, err := f.service.Read(ctx, session.Principal, asset.ID, &restored); err != nil || !bytes.Equal(restored.Bytes(), body) {
			t.Fatal("restored public Media bytes differ", err)
		}
	}
	// Metadata alone is insufficient. Tamper one exact task-owned restored file,
	// then remove it; both must fail before any bytes reach the public destination.
	a := page.Assets[0]
	var target string
	for relative, sum := range mediaFiles(t, f.root) {
		if sum == a.SHA256 {
			if target != "" {
				t.Fatal("ambiguous restored test asset")
			}
			target = filepath.Join(f.root, relative)
		}
	}
	if target == "" {
		t.Fatal("restored asset file not found in private target")
	}
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(original)
	corrupt[len(corrupt)-1] ^= 1
	if err := os.WriteFile(target, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	var escaped bytes.Buffer
	if _, err := f.service.Read(ctx, session.Principal, a.ID, &escaped); !errors.Is(err, media.ErrUnavailable) || escaped.Len() != 0 {
		t.Fatal("corrupt restored object escaped integrity validation", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Read(ctx, session.Principal, a.ID, &escaped); !errors.Is(err, media.ErrUnavailable) || escaped.Len() != 0 {
		t.Fatal("incomplete restore served missing object", err)
	}
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Read(ctx, session.Principal, a.ID, &escaped); err != nil || !bytes.Equal(escaped.Bytes(), original) {
		t.Fatal("repaired private restore remained unavailable", err)
	}
}
