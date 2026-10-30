// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPostgresAuditMigrationPhaseEvidence(t *testing.T) {
	for _, tc := range []struct{ name, sql, reason string }{
		{"missing ledger entry", "DELETE FROM audit.schema_migrations", "ledger_incomplete"},
		{"incompatible version", "UPDATE audit.schema_migrations SET version=version+2", "migration_version_incompatible"},
		{"noncontiguous ledger", "DELETE FROM audit.schema_migrations WHERE version=1", "ledger_noncontiguous"},
		{"changed checksum", "UPDATE audit.schema_migrations SET checksum=repeat('0',64)", "migration_checksum_changed"},
		{"missing runtime column", "ALTER TABLE audit.records DROP COLUMN authority", "schema_incomplete"},
		{"disabled guard", "ALTER TABLE audit.records DISABLE TRIGGER records_reject_mutation", "guard_missing"},
		{"missing attribution column", "ALTER TABLE audit.records DROP COLUMN client_id", "schema_incomplete"},
		{"missing attribution constraint", "ALTER TABLE audit.records DROP CONSTRAINT records_attribution_complete", "constraint_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := testDSN(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			if _, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
				t.Fatal(err)
			}
			if err = Migrate(ctx, dsn); err != nil {
				t.Fatal(err)
			}
			if _, err = conn.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			var logs diagnosticLog
			var protected error
			err = MigrateWithDiagnostics(ctx, dsn, slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, operation, phase string, cause error) {
				if operation != "migrate" || phase != "check_schema" {
					t.Error("migration lost failing phase")
				}
				protected = cause
			})
			if !errors.Is(err, ErrUnavailable) || protected == nil || !strings.Contains(logs.String(), tc.reason) {
				t.Fatal("schema failure was not usefully diagnosed", err, logs.String())
			}
			var internal *diagnosticError
			if errors.As(err, &internal) {
				t.Fatal("private migration object escaped")
			}
		})
	}
}
func TestPostgresAuditMigrationLockDeadlineEvidence(t *testing.T) {
	dsn := testDSN(t)
	setup, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSetup()
	conn, err := pgx.Connect(setup, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(setup)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(setup, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		t.Fatal(err)
	}
	var logs diagnosticLog
	type observation struct {
		phase string
		cause error
	}
	var observations []observation
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = MigrateWithDiagnostics(ctx, dsn, slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, _ string, phase string, cause error) {
		observations = append(observations, observation{phase, cause})
	})
	primary := false
	for _, observed := range observations {
		if observed.phase == "lock" && errors.Is(observed.cause, context.DeadlineExceeded) {
			primary = true
		}
		if observed.phase != "lock" && observed.phase != "rollback" {
			t.Errorf("unexpected migration failure phase %q", observed.phase)
		}
		if observed.cause == nil {
			t.Error("migration diagnostic lost its original cause")
		}
	}
	if !errors.Is(err, ErrUnavailable) || !primary || !strings.Contains(logs.String(), `"phase":"lock"`) || !strings.Contains(logs.String(), `"outcome":"not_committed"`) || !strings.Contains(logs.String(), "dependency_deadline") {
		t.Fatal("lock timeout lost primary phase/cause/outcome", logs.String())
	}
}
