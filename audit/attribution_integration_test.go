// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const immutableV1Checksum = "40d1269a3e3eecd83994196a373892e601b9e6e5e56177cd079f3e79f7c82c74"
const legacyRecordID = "abcdefghijklmnopqrstuvwxyz"

func installRetainedV1(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	sql, checksum := migrationIdentity()
	if checksum != immutableV1Checksum {
		t.Fatal("historical001 bytes changed")
	}
	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO audit.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO audit.records(id,actor,action,target,authority,outcome,occurred_at) VALUES($1,'operator','identity.account.create','ACCOUNT','achrix.identity.account.create','succeeded','2026-10-01T01:02:03.456789Z')", legacyRecordID); err != nil {
		t.Fatal(err)
	}
}

func retainedV1Projection(t *testing.T, ctx context.Context, conn *pgx.Conn) string {
	t.Helper()
	var result string
	if err := conn.QueryRow(ctx, "SELECT (to_jsonb(r)-ARRAY['client_id','connection_id','channel','request_id','operation'])::text FROM audit.records r WHERE id=$1", legacyRecordID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func attributionService(t *testing.T, dsn string) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := NewPostgres(dsn, Config{MaxConns: 1, MaxOperations: 2}, logger)
	if err != nil {
		t.Fatal(err)
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: 3 * time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, _, _ string) error {
		if actor == "operator" || actor == "other" {
			return nil
		}
		return achrix.ErrDenied
	}), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	s, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPostgresAuditV1UpgradeAndPreparedAttribution(t *testing.T) {
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	installRetainedV1(t, ctx, conn)
	before := retainedV1Projection(t, ctx, conn)
	old, err := NewPostgres(dsn, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Start(ctx); err == nil {
		t.Fatal("current runtime started on an unmigrated v1 ledger")
	}
	if err := old.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// A forged historical checksum must not be repaired or upgraded.
	if _, err := conn.Exec(ctx, "UPDATE audit.schema_migrations SET checksum=repeat('0',64)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, dsn); err == nil {
		t.Fatal("tampered v1 ledger upgraded")
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM information_schema.columns WHERE table_schema='audit' AND table_name='records' AND column_name='client_id'", 0)
	if _, err := conn.Exec(ctx, "UPDATE audit.schema_migrations SET checksum=$1 WHERE version=1", immutableV1Checksum); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- Migrate(ctx, dsn) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent upgrade/reentry failed", err)
		}
	}
	if version, err := ledgerVersion(ctx, conn, migrationPlan()); err != nil || version != 2 {
		t.Fatal("upgrade ledger differs", version, err)
	}
	if after := retainedV1Projection(t, ctx, conn); after != before {
		t.Fatal("upgrade rewrote historical identity, time or sequence", before, after)
	}
	s := attributionService(t, dsn)
	page, err := s.Query(ctx, "operator", "ACCOUNT", "", 100)
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != legacyRecordID || page.Records[0].Attribution != nil {
		t.Fatal("historical record acquired fictional provenance", page, err)
	}
	value := fixtureAttribution()
	expected := value
	bound, err := WithAttribution(ctx, "operator", value)
	if err != nil {
		t.Fatal(err)
	}
	value.ClientID = "not-the-snapshot"
	event := Event{Action: "identity.account.set-enabled", Target: "ACCOUNT", Authority: "achrix.identity.account.set-enabled", Outcome: "succeeded"}
	prepared, err := s.Prepare(bound, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(bound, "other", event); !errors.Is(err, ErrInput) {
		t.Fatal("bound provenance transferred to another authorized actor", err)
	}
	changed := expected
	changed.Channel = "http"
	changed.RequestID = "ANOTHER_REQUEST"
	otherContext, err := WithAttribution(ctx, "other", changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE fixture_attribution_domain(id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO fixture_attribution_domain(id) VALUES('committed')"); err != nil {
		t.Fatal(err)
	}
	if err := AppendInTx(otherContext, tx, prepared); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Export(ctx, "operator", "ACCOUNT", "", 100)
	if err != nil || len(page.Records) != 2 || !reflect.DeepEqual(page.Records[1].Attribution, &expected) || page.Records[1].Actor != "operator" {
		t.Fatal("prepared provenance changed at transaction append", page, err)
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM fixture_attribution_domain", 1)
	if _, err := conn.Exec(ctx, "UPDATE audit.records SET client_id='rewritten'"); err == nil {
		t.Fatal("new provenance bypassed immutable record guard")
	}
	// Failed native append rolls back the domain write and the whole tuple.
	if _, err := conn.Exec(ctx, "CREATE FUNCTION audit.fixture_attribution_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_ATTRIBUTION_APPEND'; END $$; CREATE TRIGGER fixture_attribution_reject BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.fixture_attribution_reject()"); err != nil {
		t.Fatal(err)
	}
	prepared, err = s.Prepare(bound, "operator", event)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO fixture_attribution_domain(id) VALUES('rolled-back')"); err != nil {
		t.Fatal(err)
	}
	if err := AppendInTx(ctx, tx, prepared); !errors.Is(err, ErrUnavailable) {
		t.Fatal("actual append failure was not safely mapped", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, conn, "SELECT count(*) FROM fixture_attribution_domain", 1)
	assertCount(t, ctx, conn, "SELECT count(*) FROM audit.records", 2)
	if _, err := conn.Exec(ctx, "DROP TRIGGER fixture_attribution_reject ON audit.records; DROP FUNCTION audit.fixture_attribution_reject()"); err != nil {
		t.Fatal(err)
	}
	plain, err := s.Append(ctx, "operator", event)
	if err != nil || plain.Attribution != nil {
		t.Fatal("direct append lost truthful absence", plain, err)
	}
	withoutConnection := expected
	withoutConnection.ConnectionID = ""
	withoutConnection.ClientID = strings.Repeat("é", 128)
	bound, err = WithAttribution(ctx, "operator", withoutConnection)
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.Append(bound, "operator", event)
	if err != nil || !reflect.DeepEqual(last.Attribution, &withoutConnection) {
		t.Fatal("bounded UTF-8 or absent connection did not persist", last, err)
	}
	page, err = s.Query(ctx, "operator", "ACCOUNT", "", 100)
	if err != nil || len(page.Records) != 4 || !reflect.DeepEqual(page.Records[3].Attribution, &withoutConnection) {
		t.Fatal("attribution SQL NULL/string roundtrip changed", page, err)
	}
	// SQL must reject partial tuples rather than accepting NULL-valued CHECK
	// expressions. Direct writes here are owned schema-defense fixtures only.
	for _, client := range []string{"client", "client\ncontrol", "client\u0085control", strings.Repeat("é", 129)} {
		var channel, request, operation any = "http", "REQUEST_123", "achrix.identity.account.set-enabled"
		if client == "client" {
			channel = nil
		}
		_, err := conn.Exec(ctx, "INSERT INTO audit.records(id,actor,action,target,authority,outcome,occurred_at,client_id,channel,request_id,operation) VALUES($1,'operator','identity.account.create','ACCOUNT','achrix.identity.account.create','succeeded',now(),$2,$3,$4,$5)", strings.Repeat("a", 26), client, channel, request, operation)
		if err == nil {
			t.Fatal("database admitted partial, control-bearing or oversized attribution")
		}
	}
}

func TestPostgresAuditUpgradeFailuresRollbackDDLAndLedger(t *testing.T) {
	for _, tc := range []struct{ name, setup, cleanup, phase string }{
		{"DDL conflict", "ALTER TABLE audit.records ADD CONSTRAINT records_attribution_complete CHECK (true)", "ALTER TABLE audit.records DROP CONSTRAINT records_attribution_complete", "ddl"},
		{"ledger rejection", "CREATE FUNCTION audit.fixture_reject_v2() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.version=2 THEN RAISE EXCEPTION 'PRIVATE_UPGRADE_TRIGGER'; END IF; RETURN NEW; END $$; CREATE TRIGGER fixture_reject_v2 BEFORE INSERT ON audit.schema_migrations FOR EACH ROW EXECUTE FUNCTION audit.fixture_reject_v2()", "DROP TRIGGER fixture_reject_v2 ON audit.schema_migrations; DROP FUNCTION audit.fixture_reject_v2()", "record_ledger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := testDSN(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			installRetainedV1(t, ctx, conn)
			before := retainedV1Projection(t, ctx, conn)
			if _, err := conn.Exec(ctx, tc.setup); err != nil {
				t.Fatal(err)
			}
			var logs diagnosticLog
			var protected error
			err = MigrateWithDiagnostics(ctx, dsn, slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, operation, phase string, cause error) {
				if operation == "migrate" && phase == tc.phase {
					protected = cause
				}
			})
			if !errors.Is(err, ErrUnavailable) || protected == nil || strings.Contains(err.Error()+logs.String(), "PRIVATE_UPGRADE_TRIGGER") || !strings.Contains(logs.String(), `"outcome":"not_committed"`) {
				t.Fatal("failed upgrade lost safe phase/cause/rollback evidence", err, logs.String())
			}
			var provider *pgconn.PgError
			if !errors.As(protected, &provider) || tc.phase == "record_ledger" && provider.Code != "P0001" {
				t.Fatal("original PostgreSQL migration cause was discarded", protected)
			}
			if version, err := ledgerVersion(ctx, conn, migrationPlan()); err != nil || version != 1 {
				t.Fatal("failed upgrade advanced the ledger", version, err)
			}
			assertCount(t, ctx, conn, "SELECT count(*) FROM information_schema.columns WHERE table_schema='audit' AND table_name='records' AND column_name IN ('client_id','connection_id','channel','request_id','operation')", 0)
			if retainedV1Projection(t, ctx, conn) != before {
				t.Fatal("failed upgrade changed retained data")
			}
			if _, err := conn.Exec(ctx, tc.cleanup); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, dsn); err != nil {
				t.Fatal("upgrade could not reenter after correcting the precondition", err)
			}
			if err := Migrate(ctx, dsn); err != nil {
				t.Fatal("current-schema verification was not repeatable", err)
			}
			if retainedV1Projection(t, ctx, conn) != before {
				t.Fatal("reentry rewrote legacy data")
			}
		})
	}
}
