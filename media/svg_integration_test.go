// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

func TestImmutableMediaMigrationPrefix(t *testing.T) {
	for name, checksum := range map[string]string{
		"001_media.sql":          "a8d31642371783c983e92b2984cd03c331cc30a0e62286fef20e1ed84130bf5d",
		"002_common_formats.sql": "ce1502dacbfb8329cf578410827b02c73ecd83c3c1d71291bb1e72967c8378e0",
	} {
		body, err := migrations.ReadFile("migrations/" + name)
		sum := sha256.Sum256(body)
		if err != nil || hex.EncodeToString(sum[:]) != checksum {
			t.Fatal("published migration bytes changed", name, err)
		}
	}
}

func installV2(t *testing.T, f *fixture) {
	t.Helper()
	ctx, cancel := operationContext(t)
	defer cancel()
	if err := f.app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "DROP SCHEMA media CASCADE"); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrationPlan()[:2] {
		if _, err := f.db.Exec(ctx, migration.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(ctx, "INSERT INTO media.schema_migrations VALUES($1,$2)", i+1, migration.checksum); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSVGRetainedV2UpgradeAndRestrictedRestart(t *testing.T) {
	f := newFixture(t)
	installV2(t, f)
	ctx, cancel := operationContext(t)
	defer cancel()
	// Real immutable v0.2 schema with retained ready PNG and opaque PDF rows.
	// Preserve each complete original, timestamp, revision and metadata through
	// the additive upgrade, then read them through the public new composition.
	retained := make(map[Asset][]byte)
	for _, sample := range []struct {
		name, mime    string
		body          []byte
		width, height int
	}{
		{"retained.png", "image/png", imageBytes(t, "png", 3, 2), 3, 2},
		{"retained.pdf", "application/pdf", formatFixture(t, Format{MIME: "application/pdf", Extensions: []string{"pdf"}}), 0, 0},
	} {
		sum := sha256.Sum256(sample.body)
		a := Asset{ID: newID(), State: "ready", Revision: 7, Filename: sample.name, MIME: sample.mime, Size: int64(len(sample.body)), Width: sample.width, Height: sample.height, SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		if err := os.WriteFile(f.root+"/"+a.ID, sample.body, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(ctx, "INSERT INTO media.assets("+assetColumns+") VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", a.ID, a.State, a.Revision, a.Filename, a.MIME, a.Size, a.Width, a.Height, a.SHA256, a.CreatedAt); err != nil {
			t.Fatal(err)
		}
		retained[a] = sample.body
	}
	before := schemaSnapshot(t, f)
	m, err := NewPostgres(f.dsn, Config{StorageRoot: f.root, AllowedMIMEs: []string{"image/svg+xml"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("new source accepted or migrated v0.2 ledger", err)
	}
	_ = m.Stop(ctx)
	if schemaSnapshot(t, f) != before {
		t.Fatal("startup changed retained v0.2 schema or metadata")
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 6)
	for range 6 {
		wg.Go(func() { outcomes <- Migrate(ctx, f.dsn) })
	}
	wg.Wait()
	close(outcomes)
	for err := range outcomes {
		if err != nil {
			t.Fatal("concurrent v0.2 retained upgrade", err)
		}
	}
	// This is the unchanged v0.2 ledger reader with its exact two-entry plan.
	// An updated database cannot be silently opened by source rolled back to it.
	if _, err := ledgerVersion(ctx, f.db, migrationPlan()[:2]); !errors.Is(err, ErrConfiguration) {
		t.Fatal("v0.2 exact ledger reader accepted the new schema", err)
	}
	for a, body := range retained {
		stored, err := scanAsset(f.db.QueryRow(ctx, "SELECT "+assetColumns+" FROM media.assets WHERE id=$1", a.ID))
		original, readErr := os.ReadFile(f.root + "/" + a.ID)
		if err != nil || stored != a || readErr != nil || !bytes.Equal(original, body) {
			t.Fatal("v0.2 upgrade changed retained metadata or bytes", err, readErr)
		}
	}
	compose := func(selection []string) (*achrix.Application, *Service) {
		t.Helper()
		m, err := NewPostgres(f.dsn, Config{StorageRoot: f.root, AllowedMIMEs: selection}, nil)
		if err != nil {
			t.Fatal(err)
		}
		policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
			if actor == testActor || actor == "list-only" && capability == List && target == LibraryTarget {
				return nil
			}
			return achrix.ErrDenied
		})
		app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}, policy, m)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cleanup, stop := operationContext(t); defer stop(); _ = app.Shutdown(cleanup) })
		service, err := NewService(app, m)
		if err != nil {
			t.Fatal(err)
		}
		return app, service
	}
	app, service := compose([]string{"image/svg+xml"})
	body := []byte(svgOriginal)
	a, err := service.Create(ctx, testActor, "retained.svg", bytes.NewReader(body))
	sum := sha256.Sum256(body)
	if err != nil || a.MIME != "image/svg+xml" || a.Width != 0 || a.Height != 0 || a.Size != int64(len(body)) || a.SHA256 != hex.EncodeToString(sum[:]) || a.Revision != 2 || a.State != "ready" {
		t.Fatal("private SVG publication metadata", a, err)
	}
	retained[a] = body
	if err := app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, restricted := compose(nil) // PNG/JPEG-only future uploads, retained reads.
	if _, err := restricted.Create(ctx, testActor, "new.svg", bytes.NewReader(body)); !errors.Is(err, ErrInput) {
		t.Fatal("restricted restart admitted new SVG", err)
	}
	page, err := restricted.List(ctx, "list-only", "", 100)
	if err != nil || len(page.Assets) != len(retained) {
		t.Fatal("retained collection after restricted restart", err)
	}
	var denied bytes.Buffer
	if _, err := restricted.Read(ctx, "list-only", a.ID, &denied); !errors.Is(err, achrix.ErrDenied) || denied.Len() != 0 {
		t.Fatal("SVG collection grant widened byte rights", err)
	}
	for a, body := range retained {
		var output bytes.Buffer
		if found, err := restricted.Read(ctx, testActor, a.ID, &output); err != nil || found != a || !bytes.Equal(output.Bytes(), body) {
			t.Fatal("retained originals after restricted restart", a.MIME, err)
		}
	}
	// Complete stored integrity must be verified before SVG read output too.
	if err := os.WriteFile(f.root+"/"+a.ID, bytes.Repeat([]byte{'x'}, len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	var corrupt bytes.Buffer
	if _, err := restricted.Read(ctx, testActor, a.ID, &corrupt); !errors.Is(err, ErrUnavailable) || corrupt.Len() != 0 {
		t.Fatal("corrupt SVG reached destination", err)
	}
	if err := os.WriteFile(f.root+"/"+a.ID, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restricted.Delete(ctx, testActor, a.ID, a.Revision); err != nil {
		t.Fatal("retained SVG deletion", err)
	}
	deleted, err := restricted.Status(ctx, testActor, a.ID)
	if err != nil || deleted.State != "deleted" || deleted.MIME != "" || deleted.Size != 0 || deleted.SHA256 != "" || deleted.Width != 0 || deleted.Height != 0 {
		t.Fatal("private SVG tombstone", deleted, err)
	}
	if _, err := os.Stat(f.root + "/" + a.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted SVG original remained", err)
	}
}

func TestSVGUpgradeDDLFailureRollsBack(t *testing.T) {
	f := newFixture(t)
	installV2(t, f)
	ctx, cancel := operationContext(t)
	defer cancel()
	// An inconsistent task-owned v0.2 database makes 003's new constraint fail
	// after its DROP. The old constraint, data and ledger must return together.
	if _, err := f.db.Exec(ctx, "ALTER TABLE media.assets DROP CONSTRAINT assets_mime_check; ALTER TABLE media.assets ADD CONSTRAINT assets_mime_check CHECK(true)"); err != nil {
		t.Fatal(err)
	}
	id := newID()
	if _, err := f.db.Exec(ctx, "INSERT INTO media.assets("+assetColumns+") VALUES($1,'pending',1,'','application/x-unknown',0,0,0,'',now())", id); err != nil {
		t.Fatal(err)
	}
	before := schemaSnapshot(t, f)
	if err := Migrate(ctx, f.dsn); !errors.Is(err, ErrUnavailable) {
		t.Fatal("invalid retained row did not fail 003", err)
	}
	if schemaSnapshot(t, f) != before {
		t.Fatal("failed 003 left partial constraint, ledger or metadata")
	}
	if _, err := f.db.Exec(ctx, "UPDATE media.assets SET mime='' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, f.dsn); err != nil {
		t.Fatal("explicit 003 retry after repair", err)
	}
}
