// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

func imageBytes(t testing.TB, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
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
func testStorage(t testing.TB) *localStorage {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := openStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.close() })
	return st
}
func TestInputAndConfigurationBounds(t *testing.T) {
	for _, name := range []string{"", "../x", "x/y", "x\\y", ".", "..", " space", "x\x00", "x\u202e.png", "x\u061c.png", "x\u200e.png", "x\u200f.png", "x\u2067.png", strings.Repeat("a", 257)} {
		if validFilename(name) {
			t.Fatalf("accepted unsafe filename %q", name)
		}
	}
	if !validFilename("تصویر نمونه.png") || !validFilename("تصویر\u200cنمونه.png") || !validFilename("تصویر\u200dنمونه.png") {
		t.Fatal("ordinary RTL filename rejected")
	}
	for _, id := range []string{"", "../x", strings.Repeat("A", 25), strings.Repeat("A", 27), strings.Repeat("a", 26)} {
		if validID(id) {
			t.Fatal("unsafe id accepted")
		}
	}
	id := newID()
	c := encodeCursor(id)
	got, err := decodeCursor(c)
	if err != nil || got != id {
		t.Fatal("cursor roundtrip")
	}
	if _, err = decodeCursor(c + "A"); !errors.Is(err, ErrInput) {
		t.Fatal("cursor length")
	}
	for _, dsn := range []string{"host=example.org sslmode=disable", "host=example.org sslmode=require", "host=example.org sslmode=prefer", "host=localhost,example.org sslmode=disable"} {
		if _, err = databaseConfig(dsn); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe remote/fallback allowed")
		}
	}
	dbConfig, err := databaseConfig("host=example.org sslmode=verify-full pool_max_conns=99 synchronous_commit=off")
	if err != nil || dbConfig.MaxConns != 4 || dbConfig.ConnConfig.RuntimeParams["synchronous_commit"] != "on" {
		t.Fatal("verified TLS/fixed pool")
	}
	if safeHostTLS("example.org", &tls.Config{InsecureSkipVerify: true, ServerName: "example.org"}) {
		t.Fatal("insecure TLS accepted")
	}
	dir := t.TempDir()
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = openStorage(dir); !errors.Is(err, ErrConfiguration) {
		t.Fatal("public root accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err = openStorage(link); !errors.Is(err, ErrConfiguration) {
		t.Fatal("symlink root accepted")
	}
}
func TestFullDecodeAndBoundedUpload(t *testing.T) {
	st := testStorage(t)
	m := &Module{decoders: make(chan struct{}, 2)}
	// Fixture encoding is deliberately outside the operation deadline: under race
	// instrumentation the valid oversized-pixel fixture itself is expensive.
	data := imageBytes(t, "png", 2, 2)
	badInputs := [][]byte{[]byte("<svg/>"), data[:len(data)-12], imageBytes(t, "jpeg", 2, 2)[:100], imageBytes(t, "png", MaxDimension+1, 1), imageBytes(t, "png", MaxDimension, MaxPixels/MaxDimension+1)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, format := range []string{"png", "jpeg"} {
		a, err := st.write(ctx, m, Asset{ID: newID()}, bytes.NewReader(imageBytes(t, format, 3, 2)))
		if err != nil || a.Width != 3 || a.Height != 2 || a.MIME != "image/"+format {
			t.Fatalf("valid %s: %v %+v", format, err, a)
		}
	}
	for _, bad := range badInputs {
		_, err := st.write(ctx, m, Asset{ID: newID()}, bytes.NewReader(bad))
		if !errors.Is(err, ErrInput) {
			t.Fatalf("bad image accepted %v", err)
		}
	}
	r := &countingReader{r: io.LimitReader(zeroReader{}, MaxUploadBytes+1000)}
	_, err := st.write(ctx, m, Asset{ID: newID()}, r)
	if !errors.Is(err, ErrInput) || r.n != MaxUploadBytes+1 {
		t.Fatalf("unbounded input %d %v", r.n, err)
	}
	m.decoders <- struct{}{}
	m.decoders <- struct{}{}
	_, err = st.write(ctx, m, Asset{ID: newID()}, bytes.NewReader(data))
	if !errors.Is(err, ErrLimited) {
		t.Fatal("decoder queue accepted")
	}
	<-m.decoders
	<-m.decoders
	expired, stop := context.WithCancel(ctx)
	stop()
	file, openErr := st.root.OpenFile(newID()+".upload", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = file.Close() }()
	if _, _, _, err := m.validate(expired, file); !errors.Is(err, context.Canceled) {
		t.Fatal("validation cancellation became input failure", err)
	}
	_, err = st.write(expired, m, Asset{ID: newID()}, bytes.NewReader(data))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, e := r.r.Read(p)
	r.n += int64(n)
	return n, e
}
func TestExclusivePublicationAndContainment(t *testing.T) {
	st := testStorage(t)
	m := &Module{decoders: make(chan struct{}, 2)}
	id := newID()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := st.unused(id); err != nil {
		t.Fatal("unused generated key", err)
	}
	if err := st.root.WriteFile(id, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.unused(id); !errors.Is(err, ErrUnavailable) {
		t.Fatal("orphaned final storage identity accepted", err)
	}
	_, err := st.write(ctx, m, Asset{ID: id}, bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err == nil {
		t.Fatal("existing destination overwritten")
	}
	b, err := st.root.ReadFile(id)
	if err != nil || string(b) != "retained" {
		t.Fatal("retained destination changed")
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := newID()
	if err = st.root.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err = st.open(symlink); err == nil {
		t.Fatal("symlink read allowed")
	}
	hard := newID()
	if err = os.Link(outside, filepath.Join(st.root.Name(), hard)); err != nil {
		t.Fatal(err)
	}
	if _, err = st.open(hard); err == nil {
		t.Fatal("external hardlink read allowed")
	}
	if err = st.remove(symlink); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(outside)
	if err != nil || string(b) != "secret" {
		t.Fatal("outside object altered")
	}
}
func TestFilesystemLockAcrossIndependentInstances(t *testing.T) {
	st := testStorage(t)
	other, err := openStorage(st.root.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.close() }()
	release, err := st.lock(true)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := other.lock(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.lock(false); !errors.Is(err, ErrConflict) {
		t.Fatal("cleanup allowed during transfer")
	}
	shared()
	release()
	exclusive, err := other.lock(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.lock(true); !errors.Is(err, ErrConflict) {
		t.Fatal("transfer allowed during cleanup")
	}
	exclusive()
	// Directory descriptor remains a real Linux directory supporting flock/fsync.
	if err = syscall.Flock(int(st.directory.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Flock(int(st.directory.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
}
func BenchmarkValidatePNG16(b *testing.B) {
	st := testStorage(b)
	img := image.NewNRGBA64(image.Rect(0, 0, 4096, 2048))
	img.SetNRGBA64(0, 0, color.NRGBA64{R: 65535, A: 65535})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		b.Fatal(err)
	}
	f, err := st.root.OpenFile(newID()+".upload", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data.Bytes()); err != nil {
		b.Fatal(err)
	}
	m := &Module{decoders: make(chan struct{}, 2)}
	b.ReportAllocs()
	b.SetBytes(int64(len(data.Bytes())))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, _, err = m.validate(ctx, f)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkCopy10MiB(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(MaxUploadBytes)
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := copyBounded(ctx, io.Discard, zeroReader{}, MaxUploadBytes)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestBoundedSafeOperationalDiagnostics(t *testing.T) {
	var log bytes.Buffer
	m := &Module{logger: slog.New(slog.NewJSONHandler(&log, nil))}
	ctx := context.Background()
	for _, err := range []error{ErrInput, ErrConflict, ErrLimited, ErrNotFound, context.Canceled, context.DeadlineExceeded} {
		if got := m.failure(ctx, "create_input", err); !errors.Is(got, err) {
			t.Fatal("routine error changed", got)
		}
	}
	if m.FailureCount() != 0 || log.Len() != 0 {
		t.Fatal("routine input/cancellation became diagnostics")
	}
	unknown := errors.Join(ErrUnknownOutcome, context.Canceled)
	for range 2 {
		if got := m.failure(ctx, "create_publish", unknown); !errors.Is(got, ErrUnknownOutcome) || !errors.Is(got, context.Canceled) {
			t.Fatal("unknown outcome/cancel not preserved", got)
		}
	}
	if m.FailureCount() != 2 || strings.Count(log.String(), "media operation failed") != 1 || !strings.Contains(log.String(), "unknown_outcome") {
		t.Fatal("unknown diagnostics not counted/bounded", log.String())
	}
	m.lastDiagnostic.Store(0)
	if got := m.failure(ctx, "read_storage", errors.New("password=secret filename=private /outside/path")); !errors.Is(got, ErrUnavailable) {
		t.Fatal("unsafe dependency error returned", got)
	}
	for _, private := range []string{"secret", "private", "/outside/path", "password="} {
		if strings.Contains(log.String(), private) {
			t.Fatal("private dependency details logged")
		}
	}
	if m.FailureCount() != 3 || strings.Count(log.String(), "media operation failed") != 2 {
		t.Fatal("operational diagnostics missing")
	}
}

func TestDescriptorAndConstructorWithoutIO(t *testing.T) {
	m, err := NewPostgres("host=localhost sslmode=disable", Config{StorageRoot: "/missing-product-owned-media-root"}, nil)
	if err != nil {
		t.Fatal("constructor performed storage/database IO", err)
	}
	d := m.Descriptor()
	if d.ID != "achrix.media" || d.Version != achrix.Version() || len(d.Requires) != 1 || d.Requires[0].ID != "achrix.authorization" || d.Requires[0].Version != 2 || len(d.Provides) != 6 || len(d.Optional) != 0 {
		t.Fatal("composition/source identity", d)
	}
	wanted := map[string]uint32{Create: 1, List: 1, Read: 1, Delete: 1, Reconcile: 1, PreparePublicImage: 1}
	for _, capability := range d.Provides {
		version, ok := wanted[capability.ID]
		if !ok || capability.Version != version {
			t.Fatal("unexpected capability ABI", capability)
		}
		delete(wanted, capability.ID)
	}
	if len(wanted) != 0 {
		t.Fatal("missing capabilities", wanted)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatal("unstarted cleanup", err)
	}
}
func BenchmarkStorageHashCopy10MiB(b *testing.B) {
	st := testStorage(b)
	f, err := st.root.OpenFile(newID(), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err = copyBounded(context.Background(), f, zeroReader{}, MaxUploadBytes); err != nil {
		b.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(MaxUploadBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		h := sha256.New()
		if _, err = copyBounded(ctx, h, f, MaxUploadBytes); err != nil {
			b.Fatal(err)
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			b.Fatal(err)
		}
		if _, err = copyBounded(ctx, io.Discard, f, MaxUploadBytes); err != nil {
			b.Fatal(err)
		}
		cancel()
	}
}

func TestMediaResourceConfigurationAndOwnership(t *testing.T) {
	const dsn = "host=127.0.0.1 port=1 user=fixture dbname=fixture sslmode=disable pool_max_conns=128 pool_min_conns=8 pool_min_idle_conns=8 synchronous_commit=off"
	for _, config := range []Config{{}, {MaxConns: 4, MaxOperations: 4}, {MaxConns: 1, MaxOperations: 1}, {MaxConns: 6, MaxOperations: 8}, {MaxConns: 6, MaxOperations: 1}, {MaxConns: 1<<31 - 1, MaxOperations: int(^uint(0) >> 1)}} {
		config.StorageRoot = "/missing-product-owned-media-root"
		config.AllowedMIMEs = []string{"image/png", "image/svg+xml"}
		m, err := NewPostgres(dsn, config, nil)
		if err != nil {
			t.Fatal("valid resource configuration rejected", err)
		}
		wantConns, wantOperations := config.MaxConns, config.MaxOperations
		if wantConns == 0 {
			wantConns = 4
		}
		if wantOperations == 0 {
			wantOperations = 4
		}
		config.MaxConns, config.MaxOperations = 1, 1
		config.AllowedMIMEs[0] = "application/pdf"
		if m.config.MaxConns != wantConns || m.dbConfig.MaxConns != wantConns || m.config.MaxOperations != wantOperations || m.dbConfig.MinConns != 0 || m.dbConfig.MinIdleConns != 0 || m.pool != nil || m.storage != nil || m.state != "new" || !m.allows("image/png") || cap(m.decoders) != 2 || m.dbConfig.ConnConfig.RuntimeParams["synchronous_commit"] != "on" {
			t.Fatal("DSN precedence, copied config, decoder/durability bounds or construction changed")
		}
		if err := m.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []Config{{MaxConns: -1}, {MaxOperations: -1}} {
		config.StorageRoot = "/missing-product-owned-media-root"
		if m, err := NewPostgres(dsn, config, nil); m != nil || !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid resource configuration admitted", err)
		}
	}
}
