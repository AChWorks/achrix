// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const publicImageActor achrix.Principal = "public-image-preparer"

func TestPostgresPublicImagePermissionSnapshotAndFailures(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	for i := 0; i < len(pixels.Pix); i += 4 {
		copy(pixels.Pix[i:], []byte{21, 42, 63, 128})
	}
	source := publicPNG(t, pixels, pngChunk("eXIf", orientationExif(6, true)))
	asset, err := f.service.Create(ctx, testActor, "original-private.png", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	request := PreparePublicImageRequest{AssetID: asset.ID, ExpectedRevision: asset.Revision}
	var output bytes.Buffer
	for _, actor := range []achrix.Principal{testActor, "denied"} {
		if _, err = f.service.PreparePublicImage(ctx, actor, request, &output); !errors.Is(err, achrix.ErrDenied) || output.Len() != 0 {
			t.Fatal("old grants implied prepare", err)
		}
	}
	unknown := request
	unknown.AssetID = newID()
	if _, err = f.service.PreparePublicImage(ctx, "denied", unknown, &output); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denial inspected source", err)
	}
	if _, err = f.service.Read(ctx, publicImageActor, asset.ID, &output); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("prepare implied Read", err)
	}
	result, err := f.service.PreparePublicImage(ctx, publicImageActor, request, &output)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(output.Bytes())
	if result.SourceAssetID != asset.ID || result.SourceRevision != asset.Revision || result.SourceSHA256 != asset.SHA256 || result.SHA256 != hex.EncodeToString(sum[:]) || result.Size != int64(output.Len()) || result.Profile != PublicImageProfile || result.ColorBasis != "declared-srgb" || result.Width != 3 || result.Height != 2 || result.MIME != "image/png" {
		t.Fatal("result binding", result)
	}
	var original bytes.Buffer
	retained, err := f.service.Read(ctx, testActor, asset.ID, &original)
	if err != nil || retained != asset || !bytes.Equal(source, original.Bytes()) {
		t.Fatal("private original changed", err)
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 1 {
		t.Fatal("derivative persisted", entries, err)
	} // The original is the only stored file.
	output.Reset()
	pendingID := newID()
	if _, err = f.db.Exec(ctx, "INSERT INTO media.assets(id,state,revision,filename,mime,size,width,height,sha256,created_at) VALUES($1,'pending',1,'pending','',0,0,0,'',now())", pendingID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{pendingID, 1}, &output); !errors.Is(err, ErrNotFound) || output.Len() != 0 {
		t.Fatal("nonready", err)
	}
	if err = os.Truncate(filepath.Join(f.root, asset.ID), int64(len(source)-1)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, &output); !errors.Is(err, ErrUnavailable) || output.Len() != 0 {
		t.Fatal("stored size mismatch", err)
	}
	if err = os.WriteFile(filepath.Join(f.root, asset.ID), source, 0600); err != nil {
		t.Fatal(err)
	}
	stale := request
	stale.ExpectedRevision--
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, stale, &output); !errors.Is(err, ErrConflict) || output.Len() != 0 {
		t.Fatal("stale", err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, unknown, &output); !errors.Is(err, ErrNotFound) || output.Len() != 0 {
		t.Fatal("unknown", err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, failedPublicWriter{}); !errors.Is(err, ErrUnavailable) || err.Error() == "private writer detail" {
		t.Fatal("writer error leaked", err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, shortPublicWriter{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("short writer", err)
	}
	if _, err = f.db.Exec(ctx, "UPDATE media.assets SET width=3 WHERE id=$1", asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, &output); !errors.Is(err, ErrUnavailable) || output.Len() != 0 {
		t.Fatal("raw dimensions binding", err)
	}
	if _, err = f.db.Exec(ctx, "UPDATE media.assets SET width=2 WHERE id=$1", asset.ID); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), source...)
	corrupt[len(corrupt)-1] ^= 1
	if err = os.WriteFile(filepath.Join(f.root, asset.ID), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, &output); !errors.Is(err, ErrUnavailable) || output.Len() != 0 {
		t.Fatal("source integrity", err)
	}
	if err = os.WriteFile(filepath.Join(f.root, asset.ID), source, 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Delete(ctx, testActor, asset.ID, asset.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, request, &output); !errors.Is(err, ErrNotFound) || output.Len() != 0 {
		t.Fatal("deleted", err)
	}
}
func TestPostgresPublicImageUnsupportedRemainsPrivate(t *testing.T) {
	f := newFixture(t, append(CommonMIMEs(), "image/svg+xml"))
	ctx, cancel := operationContext(t)
	defer cancel()
	ordinary := imageBytes(t, "jpeg", 8, 8)
	icc := jpegSegment(226, []byte("ICC_PROFILE\x00private"))
	jpegSource := append(append(append([]byte(nil), ordinary[:2]...), icc...), ordinary[2:]...)
	for _, input := range []struct {
		name   string
		source []byte
	}{{"color-managed.jpg", jpegSource}, {"active.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>private</script></svg>`)}} {
		asset, err := f.service.Create(ctx, testActor, input.name, bytes.NewReader(input.source))
		if err != nil {
			t.Fatal("private accepted", err)
		}
		var output bytes.Buffer
		if _, err = f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, &output); !errors.Is(err, ErrInput) || output.Len() != 0 {
			t.Fatal("unsupported public", err)
		}
		if _, err = f.service.Read(ctx, testActor, asset.ID, &output); err != nil || !bytes.Equal(output.Bytes(), input.source) {
			t.Fatal("retained private", err)
		}
	}
}

type callbackPublicWriter struct {
	once     sync.Once
	callback func()
	body     bytes.Buffer
}

func (w *callbackPublicWriter) Write(p []byte) (int, error) {
	w.once.Do(w.callback)
	return w.body.Write(p)
}
func TestPostgresPublicImageUsesSingleVerifiedSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	source := imageBytes(t, "png", 4, 3)
	asset, err := f.service.Create(ctx, testActor, "snapshot.png", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	writer := &callbackPublicWriter{callback: func() {
		mutated := append([]byte(nil), source...)
		mutated[29] ^= 1
		if err := os.WriteFile(filepath.Join(f.root, asset.ID), mutated, 0600); err != nil {
			t.Error(err)
		}
	}}
	result, err := f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, writer)
	if err != nil || result.SourceSHA256 != asset.SHA256 {
		t.Fatal("verified snapshot", err)
	}
	decoded, err := png.Decode(bytes.NewReader(writer.body.Bytes()))
	if err != nil || decoded.Bounds().Dx() != 4 {
		t.Fatal("reread changed source", err)
	}
	var out bytes.Buffer
	if _, err = f.service.Read(ctx, testActor, asset.ID, &out); !errors.Is(err, ErrUnavailable) {
		t.Fatal("external mutation not detected", err)
	}
}

type blockedPublicWriter struct {
	entered chan<- struct{}
	stop    <-chan struct{}
	once    sync.Once
}

func (w *blockedPublicWriter) Write([]byte) (int, error) {
	w.once.Do(func() { w.entered <- struct{}{} })
	<-w.stop
	return 0, context.Canceled
}
func TestPostgresPublicImageSharedAdmissionAndStop(t *testing.T) {
	f := newFixtureConfig(t, Config{MaxConns: 4, MaxOperations: 8})
	ctx, cancel := operationContext(t)
	defer cancel()
	source := imageBytes(t, "png", 4, 3)
	asset, err := f.service.Create(ctx, testActor, "shared.png", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, &blockedPublicWriter{entered: entered, stop: f.module.work.Done()})
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("writers not entered")
		}
	}
	if len(f.module.decoders) != 2 {
		t.Fatal("shared admission")
	}
	var out bytes.Buffer
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, &out); !errors.Is(err, ErrLimited) || out.Len() != 0 {
		t.Fatal("third compute queued", err)
	}
	// The same slots also protect existing expensive private upload validation.
	if _, err = f.service.Create(ctx, testActor, "shared-upload.png", bytes.NewReader(source)); !errors.Is(err, ErrLimited) {
		t.Fatal("upload did not share compute limit", err)
	}
	stop, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStop()
	if err = f.app.Shutdown(stop); err != nil {
		t.Fatal("cooperative stop", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-stop.Done():
			t.Fatal("lease not drained")
		}
	}
	f.module.mu.Lock()
	active := f.module.active
	f.module.mu.Unlock()
	if active != 0 || len(f.module.decoders) != 0 {
		t.Fatal("owned admission leaked")
	}
	if _, err = f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, io.Discard); err == nil {
		t.Fatal("stopped prepare admitted")
	}
}
func TestPublicImagePNGConcreteModels(t *testing.T) {
	models := []image.Image{image.NewGray(image.Rect(0, 0, 3, 2)), image.NewRGBA(image.Rect(0, 0, 3, 2)), image.NewPaletted(image.Rect(0, 0, 3, 2), color.Palette{color.NRGBA{R: 21, A: 1}, color.NRGBA{R: 99, G: 98, B: 97, A: 0}})}
	for _, pixels := range models {
		preparedBytes(t, publicPNG(t, pixels), "image/png")
	}
}

func TestPostgresPublicImageDestinationErrorsAreSafe(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	asset, err := f.service.Create(ctx, testActor, "safe-writer.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	f.module.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	const private = "private-path password=private-credential"
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrInput, ErrConflict, ErrLimited, ErrNotFound, ErrUnavailable, ErrUnknownOutcome} {
		want := category
		if category == ErrUnknownOutcome {
			want = ErrUnavailable
		}
		result, got := f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, wrappedPublicWriter{fmt.Errorf("%s: %w", private, category)})
		if got != want || result != (PublicImage{}) {
			t.Fatal("private destination error escaped", got, result)
		}
	}
	if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "password=") {
		t.Fatal("private writer details logged")
	}
	if f.module.FailureCount() != 2 {
		t.Fatal("unexpected safe diagnostic classification", f.module.FailureCount())
	}
	f.module.mu.Lock()
	active := f.module.active
	f.module.mu.Unlock()
	if active != 0 || len(f.module.decoders) != 0 {
		t.Fatal("failed output leaked ownership")
	}
	var output bytes.Buffer
	if _, err := f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, &output); err != nil {
		t.Fatal("safe failure prevented later preparation", err)
	}
}

// The actual driver constructs a ConnectError around a private dial failure while
// the caller context remains live. Both legacy Read and preparation must expose
// only canonical safe identities and discard the driver/configuration unwrap chain.
func TestPostgresReadAndPublicImageDependencyErrorsAreSafe(t *testing.T) {
	for _, category := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(category.Error(), func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := operationContext(t)
			defer cancel()
			asset, err := f.service.Create(ctx, testActor, "dependency.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			f.module.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			config := f.module.pool.Config()
			const private = "private-connection-detail"
			config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
				return nil, fmt.Errorf("%s: %w", private, category)
			}
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			// Establish that the actual pinned driver retains private details and
			// cancellation identity before the Service boundary removes them.
			connection, dependencyErr := pool.Acquire(ctx)
			if connection != nil {
				connection.Release()
			}
			var wrapped *pgconn.ConnectError
			if !errors.Is(dependencyErr, category) || !errors.As(dependencyErr, &wrapped) || !strings.Contains(dependencyErr.Error(), private) || ctx.Err() != nil {
				pool.Close()
				t.Fatal("driver did not produce the expected wrapped private dependency error")
			}
			// No ingress/operations run concurrently in this owned fixture. Its
			// normal shutdown retains ownership of the replacement lazy pool.
			f.module.pool.Close()
			f.module.mu.Lock()
			f.module.pool = pool
			f.module.mu.Unlock()
			var privateRead bytes.Buffer
			readAsset, readErr := f.service.Read(ctx, testActor, asset.ID, &privateRead)
			var readConnectionError *pgconn.ConnectError
			if readErr != category || errors.As(readErr, &readConnectionError) || ctx.Err() != nil || readAsset != (Asset{}) || privateRead.Len() != 0 {
				t.Fatal("Read dependency failure was not canonical with a live caller", readErr, readAsset)
			}
			var output bytes.Buffer
			result, got := f.service.PreparePublicImage(ctx, publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, &output)
			var connectionError *pgconn.ConnectError
			if got != category || errors.As(got, &connectionError) || ctx.Err() != nil || result != (PublicImage{}) || output.Len() != 0 {
				t.Fatal("dependency failure was not canonical with a live caller", got, result)
			}
			if strings.Contains(logs.String(), private) || strings.Contains(got.Error(), private) || f.module.FailureCount() != 0 {
				t.Fatal("dependency error leaked or changed expected cancellation diagnostics")
			}
			f.module.mu.Lock()
			active := f.module.active
			f.module.mu.Unlock()
			if active != 0 || len(f.module.decoders) != 0 || pool.Stat().AcquiredConns() != 0 {
				t.Fatal("dependency failure leaked preparation ownership")
			}
		})
	}
}
