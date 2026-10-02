// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
)

// The validation script supplies a separate task-owned database. Package tests
// must never share Identity's schema-reset database when Go runs them in parallel.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("ACHRIX_AUDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL proof requires ACHRIX_AUDIT_TEST_DSN")
	}
	c, err := databaseConfig(dsn)
	if err != nil || !strings.HasPrefix(c.ConnConfig.Database, "achrix_audit_") {
		t.Fatal("Audit reset requires the separate task-owned achrix_audit_ database profile")
	}
	return dsn
}

func TestPostgresRetainedAudit(t *testing.T) {
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c, x := context.WithTimeout(context.Background(), time.Second); defer x(); _ = conn.Close(c) })
	if _, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Migrate(ctx, dsn) }()
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatalf("concurrent/repeated migration: %v", err)
		}
	}
	fixed := time.Date(2026, 10, 2, 1, 2, 3, 456789123, time.FixedZone("fixture", 3600))
	m, err := NewPostgres(dsn, Config{Now: func() time.Time { return fixed }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(ctx context.Context, actor achrix.Principal, capability, target string) error {
		if actor == "operator" {
			return nil
		}
		if actor == "reader" && capability == Query && target == "ACCOUNT" {
			return nil
		}
		if actor == "provider-error" {
			return errors.New("private provider failure")
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second}, policy, m)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	s, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Action: "identity.account.create", Target: "ACCOUNT", Authority: "achrix.identity.account-create", Outcome: "succeeded"}
	var expected []Record
	for i := 0; i < 5; i++ {
		r, e := s.Append(ctx, "operator", event)
		if e != nil {
			t.Fatal(e)
		}
		expected = append(expected, r)
	}
	other := event
	other.Target = "OTHER"
	if _, err = s.Append(ctx, "operator", other); err != nil {
		t.Fatal(err)
	}
	var records []Record
	cursor := ""
	for {
		p, e := s.Query(ctx, "reader", "ACCOUNT", cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, p.Records...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if !reflect.DeepEqual(records, expected) {
		t.Fatal("target-specific bounded pages lost/changed retained records")
	}
	if records[0].OccurredAt != fixed.UTC().Truncate(time.Microsecond) || records[0].Actor != "operator" {
		t.Fatal("owned actor/time metadata incorrect")
	}
	if _, err = s.Query(ctx, "reader", "OTHER", "", 1); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = s.Export(ctx, "reader", "ACCOUNT", "", 1); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = s.Query(ctx, "provider-error", "ACCOUNT", "", 1); !errors.Is(err, achrix.ErrAuthorizationUnavailable) {
		t.Fatal(err)
	}
	page, err := s.Export(ctx, "operator", "ACCOUNT", "", 100)
	if err != nil || !reflect.DeepEqual(page.Records, expected) {
		t.Fatal("bounded authorized export differs from retained dataset")
	}
	for _, sql := range []string{"UPDATE audit.records SET outcome='succeeded'", "DELETE FROM audit.records", "TRUNCATE audit.records"} {
		if _, err = conn.Exec(ctx, sql); err == nil {
			t.Fatal("retained records allowed mutation")
		}
	}
	if _, err = conn.Exec(ctx, "CREATE TEMP TABLE fixture_domain(id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.Prepare(ctx, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO fixture_domain(id) VALUES('rolled-back')"); err != nil {
		t.Fatal(err)
	}
	if err = AppendInTx(ctx, tx, prepared); err != nil {
		t.Fatal(err)
	}
	if err = AppendInTx(ctx, tx, prepared); !errors.Is(err, ErrConflict) {
		t.Fatal("preparation was reusable")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM fixture_domain", 0)
	assertCount(t, ctx, conn, "SELECT count(*) FROM audit.records WHERE target='ACCOUNT'", 5)
	if _, err = conn.Exec(ctx, "CREATE FUNCTION audit.fixture_fail_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit failure'; END $$; CREATE TRIGGER fixture_fail_insert BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.fixture_fail_insert()"); err != nil {
		t.Fatal(err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = s.Prepare(ctx, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO fixture_domain(id) VALUES('audit-failed')"); err != nil {
		t.Fatal(err)
	}
	if err = AppendInTx(ctx, tx, prepared); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM fixture_domain", 0)
	assertCount(t, ctx, conn, "SELECT count(*) FROM audit.records WHERE target='ACCOUNT'", 5)
	if _, err = conn.Exec(ctx, "DROP TRIGGER fixture_fail_insert ON audit.records; DROP FUNCTION audit.fixture_fail_insert()"); err != nil {
		t.Fatal(err)
	}
	original, originalCancel := context.WithCancel(ctx)
	prepared, err = s.Prepare(original, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	originalCancel()
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = AppendInTx(ctx, tx, prepared); !errors.Is(err, context.Canceled) {
		t.Fatal("fresh context extended canceled preparation")
	}
	_ = tx.Rollback(ctx)
	prepared, err = s.Prepare(ctx, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = AppendInTx(context.Background(), tx, prepared); !errors.Is(err, ErrConfiguration) {
		t.Fatal("append admitted without deadline")
	}
	_ = tx.Rollback(ctx)
	if _, err = conn.Exec(ctx, "UPDATE audit.schema_migrations SET checksum=repeat('0',64)"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err == nil {
		t.Fatal("changed immutable migration identity admitted")
	}
	bad, err := NewPostgres(dsn, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = bad.Start(ctx); err == nil {
		t.Fatal("runtime admitted changed migration identity")
	}
	if err = bad.Stop(ctx); err != nil {
		t.Fatal("partial-start resources did not clean up")
	}
	_, checksum := migrationIdentity()
	if _, err = conn.Exec(ctx, "UPDATE audit.schema_migrations SET checksum=$1", checksum); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "ALTER TABLE audit.records DISABLE TRIGGER records_reject_mutation"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err == nil {
		t.Fatal("disabled immutability guard admitted")
	}
	if _, err = conn.Exec(ctx, "ALTER TABLE audit.records ENABLE TRIGGER records_reject_mutation"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal("valid retained schema failed re-verification")
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM audit.records", 6)
}

func assertCount(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, want int) {
	t.Helper()
	var got int
	if err := conn.QueryRow(ctx, sql).Scan(&got); err != nil || got != want {
		t.Fatalf("persisted-state count: got %d, want %d, err %v", got, want, err)
	}
}

func TestPostgresStopCancelsOwnedAppend(t *testing.T) {
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c, x := context.WithTimeout(context.Background(), time.Second); defer x(); _ = conn.Close(c) }()
	if _, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	m, err := NewPostgres(dsn, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: 3 * time.Second, ShutdownTimeout: 2 * time.Second}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return nil }), m)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	s, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Prepare(ctx, "operator", Event{Action: "identity.account.create", Target: "ACCOUNT", Authority: "achrix.identity.account-create", Outcome: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, x := context.WithTimeout(context.Background(), time.Second)
		defer x()
		_ = lock.Rollback(c)
	}()
	if _, err = lock.Exec(ctx, "LOCK TABLE audit.records IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	writer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c, x := context.WithTimeout(context.Background(), time.Second); defer x(); _ = writer.Close(c) }()
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c, x := context.WithTimeout(context.Background(), time.Second); defer x(); _ = tx.Rollback(c) }()
	result := make(chan error, 1)
	go func() { result <- AppendInTx(ctx, tx, p) }()
	until := time.After(time.Second)
	for {
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active > 0 {
			break
		}
		select {
		case <-until:
			t.Fatal("append not admitted")
		case <-time.After(time.Millisecond):
		}
	}
	if err = app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown did not cancel owned SQL: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned append failed to drain")
	}
	if err = AppendInTx(ctx, tx, p); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stopped admission reopened")
	}
}
