// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPostgresStartupDiagnosesMissingAndIncompleteSchema(t *testing.T) {
	for _, scenario := range []struct{ mutation, phase, reason, sqlstate string }{
		{"DROP TABLE media.schema_migrations", "ledger_query", "ledger_unavailable", "42P01"},
		{"DELETE FROM media.schema_migrations WHERE version=3", "ledger_validate", "migration_required", ""},
		{"ALTER TABLE media.assets DROP COLUMN sha256 CASCADE", "asset_schema", "asset_schema_invalid", "42703"},
	} {
		t.Run(scenario.reason, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := operationContext(t)
			defer cancel()
			if _, err := f.db.Exec(ctx, scenario.mutation); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			captured := false
			m, err := NewPostgres(f.dsn, Config{StorageRoot: f.root, Diagnostic: func(_ context.Context, operation, phase string, cause error) {
				var detail *diagnosticFault
				if operation != "start_schema" || phase != scenario.phase || !errors.As(cause, &detail) || detail.reason != scenario.reason {
					t.Error("startup cause/phase lost", operation, phase, cause)
				}
				captured = true
			}}, slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Start(ctx); !errors.Is(err, ErrUnavailable) {
				t.Fatal("invalid schema admitted", err)
			}
			if err = m.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			if !captured || !strings.Contains(logs.String(), scenario.reason) || !strings.Contains(logs.String(), `"phase":"`+scenario.phase+`"`) {
				t.Fatal("startup diagnostic missing", logs.String())
			}
			if scenario.sqlstate != "" && !strings.Contains(logs.String(), `"sqlstate":"`+scenario.sqlstate+`"`) {
				t.Fatal("SQLSTATE lost", logs.String())
			}
			if strings.Contains(logs.String(), f.dsn) || strings.Contains(logs.String(), f.root) {
				t.Fatal("private configuration in ordinary diagnostics")
			}
		})
	}
}

func TestPostgresMigrationLockTimeoutHasSpecificPhase(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	if _, err := f.db.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if _, err := f.db.Exec(cleanup, "SELECT pg_advisory_unlock($1)", migrationLock); err != nil {
			t.Error("release owned migration lock", err)
		}
	}()
	var logs bytes.Buffer
	captured := false
	blocked, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	err := MigrateWithDiagnostics(blocked, f.dsn, slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, operation, phase string, cause error) {
		if operation == "migrate" && phase == "lock" && cause != nil {
			captured = true
		}
	})
	if !errors.Is(err, ErrUnavailable) || !captured || !strings.Contains(logs.String(), `"phase":"lock"`) || !strings.Contains(logs.String(), `"outcome":"not_executed"`) {
		t.Fatal("migration lock timeout obscured", err, logs.String())
	}
}
