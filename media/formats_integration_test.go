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

	"github.com/AChWorks/achrix"
	"time"
)

func TestCommonFormatPublicRoundTrips(t *testing.T) {
	f := newFixture(t, CommonMIMEs())
	ctx, cancel := operationContext(t)
	defer cancel()
	for _, format := range SupportedFormats() {
		body := formatFixture(t, format)
		a, err := f.service.Create(ctx, testActor, "sample."+format.Extensions[0], bytes.NewReader(body))
		if err != nil || a.MIME != format.MIME {
			t.Fatalf("%s create: %+v %v", format.MIME, a, err)
		}
		if format.MIME != "image/png" && format.MIME != "image/jpeg" && (a.Width != 0 || a.Height != 0) {
			t.Fatal("opaque dimensions", a)
		}
		var denied bytes.Buffer
		if _, err := f.service.Read(ctx, "denied", a.ID, &denied); err == nil || denied.Len() != 0 {
			t.Fatal("opaque authorization", format.MIME, err)
		}
		var output bytes.Buffer
		found, err := f.service.Read(ctx, testActor, a.ID, &output)
		if err != nil || found != a || !bytes.Equal(output.Bytes(), body) {
			t.Fatal("original-byte public round trip", format.MIME, err)
		}
		status, err := f.service.Status(ctx, testActor, a.ID)
		if err != nil || status != a {
			t.Fatal("public status", format.MIME, err)
		}
	}
	page, err := f.service.List(ctx, testActor, "", 100)
	if err != nil || len(page.Assets) != 36 {
		t.Fatal("common ready list", len(page.Assets), err)
	}
}

func installV1(t *testing.T, f *fixture) {
	t.Helper()
	ctx, cancel := operationContext(t)
	defer cancel()
	if err := f.app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "DROP SCHEMA media CASCADE"); err != nil {
		t.Fatal(err)
	}
	sql, checksum := migrationIdentity()
	if _, err := f.db.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "INSERT INTO media.schema_migrations VALUES(1,$1)", checksum); err != nil {
		t.Fatal(err)
	}
}
func TestRetainedV1UpgradeConcurrentLedgerAndOldSource(t *testing.T) {
	f := newFixture(t)
	installV1(t, f)
	ctx, cancel := operationContext(t)
	defer cancel()
	body := imageBytes(t, "png", 3, 2)
	sum := sha256.Sum256(body)
	a := Asset{ID: newID(), State: "ready", Revision: 2, Filename: "retained.png", MIME: "image/png", Size: int64(len(body)), Width: 3, Height: 2, SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := os.WriteFile(f.root+"/"+a.ID, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "INSERT INTO media.assets("+assetColumns+") VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", a.ID, a.State, a.Revision, a.Filename, a.MIME, a.Size, a.Width, a.Height, a.SHA256, a.CreatedAt); err != nil {
		t.Fatal(err)
	}
	// New source startup is verification only; it does not advance a v1 ledger.
	m, err := NewPostgres(f.dsn, Config{StorageRoot: f.root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("startup migrated incomplete ledger", err)
	}
	_ = m.Stop(ctx)
	var count int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM media.schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatal("startup ledger effects", count, err)
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
			t.Fatal("concurrent retained upgrade", err)
		}
	}
	// Old immutable source's exact one-entry check rejects the new ledger.
	var checksum string
	var version int
	if err := f.db.QueryRow(ctx, "SELECT count(*),min(checksum),min(version) FROM media.schema_migrations").Scan(&count, &checksum, &version); err != nil {
		t.Fatal(err)
	}
	_, oldChecksum := migrationIdentity()
	if count == 1 && version == 1 && checksum == oldChecksum {
		t.Fatal("old source ledger check silently accepts upgrade")
	}
	if count != 2 {
		t.Fatal("exact two-entry ledger", count)
	}
	stored, err := scanAsset(f.db.QueryRow(ctx, "SELECT "+assetColumns+" FROM media.assets WHERE id=$1", a.ID))
	if err != nil || stored != a {
		t.Fatal("retained metadata changed", stored, err)
	}
	retained, err := os.ReadFile(f.root + "/" + a.ID)
	if err != nil || !bytes.Equal(retained, body) {
		t.Fatal("retained v1 bytes changed", err)
	}
	// Compose the public service after migration and read the old retained bytes.
	m, err = NewPostgres(f.dsn, Config{StorageRoot: f.root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, _, _ string) error {
		if actor == testActor {
			return nil
		}
		return achrix.ErrDenied
	}), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanup, cancel := operationContext(t); defer cancel(); _ = app.Shutdown(cleanup) })
	service, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if found, err := service.Read(ctx, testActor, a.ID, &output); err != nil || found != a || !bytes.Equal(output.Bytes(), body) {
		t.Fatal("retained v1 public bytes", err)
	}

	if err = Migrate(ctx, f.dsn); err != nil {
		t.Fatal("idempotent migration", err)
	}
}
func schemaSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	ctx, cancel := operationContext(t)
	defer cancel()
	var snapshot string
	err := f.db.QueryRow(ctx, `SELECT jsonb_build_object(
 'ledger',(SELECT jsonb_agg(to_jsonb(s) ORDER BY version) FROM media.schema_migrations s),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname) FROM pg_constraint WHERE conrelid='media.assets'::regclass),
 'assets',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]'::jsonb) FROM media.assets a))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func TestMigrationRejectsUnknownChangedMissingLedgerWithoutEffects(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE media.schema_migrations SET checksum=repeat('0',64) WHERE version=1",
		"DELETE FROM media.schema_migrations WHERE version=1",
		"INSERT INTO media.schema_migrations VALUES(3,repeat('0',64))",
		"DELETE FROM media.schema_migrations",
		"UPDATE media.schema_migrations SET version=4 WHERE version=2",
	} {
		t.Run(mutation, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := operationContext(t)
			defer cancel()
			a, err := f.service.Create(ctx, testActor, "retained.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			before := schemaSnapshot(t, f)
			retained, err := os.ReadFile(f.root + "/" + a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, f.dsn); !errors.Is(err, ErrUnavailable) {
				t.Fatal("unknown ledger accepted", err)
			}
			if after := schemaSnapshot(t, f); after != before {
				t.Fatal("rejected migration changed database")
			}
			after, err := os.ReadFile(f.root + "/" + a.ID)
			if err != nil || !bytes.Equal(after, retained) {
				t.Fatal("rejected migration changed file", err)
			}
		})
	}
}
func TestMigrationDDLFailureRollsBackWholeUpgrade(t *testing.T) {
	f := newFixture(t)
	installV1(t, f)
	ctx, cancel := operationContext(t)
	defer cancel()
	// A collision after 002 has dropped/added earlier constraints proves rollback
	// of the entire ordered upgrade, not only failure before any DDL.
	if _, err := f.db.Exec(ctx, "ALTER TABLE media.assets ADD CONSTRAINT assets_dimensions_check CHECK (true)"); err != nil {
		t.Fatal(err)
	}
	before := schemaSnapshot(t, f)
	if err := Migrate(ctx, f.dsn); !errors.Is(err, ErrUnavailable) {
		t.Fatal("expected transaction failure", err)
	}
	if after := schemaSnapshot(t, f); after != before {
		t.Fatal("failed 002 left partial ledger or constraints")
	}
	if _, err := f.db.Exec(ctx, "ALTER TABLE media.assets DROP CONSTRAINT assets_dimensions_check"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, f.dsn); err != nil {
		t.Fatal("retry explicit upgrade", err)
	}
}

func TestConcurrentFreshInstall(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	if err := f.app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "DROP SCHEMA media CASCADE"); err != nil {
		t.Fatal(err)
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
			t.Fatal("concurrent fresh install", err)
		}
	}
	plan := migrationPlan()
	rows, err := f.db.Query(ctx, "SELECT version,checksum FROM media.schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			t.Fatal(err)
		}
		if count >= len(plan) || version != count+1 || checksum != plan[count].checksum {
			t.Fatal("fresh exact ledger")
		}
		count++
	}
	if rows.Err() != nil || count != 2 {
		t.Fatal("fresh ledger length", count, rows.Err())
	}
}
