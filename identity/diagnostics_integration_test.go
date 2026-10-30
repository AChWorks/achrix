// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
)

func TestPostgresCorruptCredentialRemainsPrivateAndDiagnosable(t *testing.T) {
	f := newIdentityFixture(t)
	account := f.create(t, "diagnostic-user")
	if _, err := f.service.Login(context.Background(), "diagnostic-user", "incorrect password", ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("wrong password semantics changed", err)
	}
	before := f.module.FailureCount()
	const private = "PRIVATE_CREDENTIAL_HASH_MATERIAL"
	if _, err := f.db.Exec(context.Background(), "UPDATE identity.credentials SET password_hash=$1 WHERE account_id=$2", private, account.ID); err != nil {
		t.Fatal(err)
	}
	var protected error
	f.module.config.Diagnostic = func(_ context.Context, operation, phase string, cause error) {
		if operation != "credential" || phase != "verify" {
			t.Error("credential failure lost owner/phase")
		}
		protected = cause
	}
	_, err := f.service.Login(context.Background(), "diagnostic-user", testPassword, "")
	var internal *diagnosticError
	if err != ErrUnavailable || errors.As(err, &internal) || !errors.Is(protected, argon2id.ErrInvalidHash) || errors.Is(err, argon2id.ErrInvalidHash) || f.module.FailureCount() != before+1 {
		t.Fatal("corrupt credential escaped or lost operational diagnosis")
	}
	if !strings.Contains(f.logs.String(), "credential_record_malformed") || strings.Contains(f.logs.String(), private) || strings.Contains(f.logs.String(), testPassword) || strings.Contains(protected.Error(), private) || strings.Contains(err.Error(), private) {
		t.Fatal("credential diagnosis lost reason or exposed password/hash")
	}
}
func TestPostgresIdentityMigrationPhaseEvidence(t *testing.T) {
	for _, tc := range []struct{ name, sql, reason string }{
		{"missing ledger entry", "DELETE FROM identity.schema_migrations", "ledger_incomplete"},
		{"incompatible version", "UPDATE identity.schema_migrations SET version=2", "migration_version_incompatible"},
		{"changed checksum", "UPDATE identity.schema_migrations SET checksum=repeat('0',64)", "migration_checksum_changed"},
		{"missing runtime column", "ALTER TABLE identity.credentials DROP COLUMN changed_at", "schema_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIdentityFixture(t)
			if _, err := f.db.Exec(context.Background(), tc.sql); err != nil {
				t.Fatal(err)
			}
			var logs diagnosticLog
			var protected error
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := MigrateWithDiagnostics(ctx, os.Getenv("ACHRIX_IDENTITY_TEST_DSN"), slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, operation, phase string, cause error) {
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
func TestPostgresIdentityMigrationLockDeadlineEvidence(t *testing.T) {
	f := newIdentityFixture(t)
	tx, err := f.db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
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
	err = MigrateWithDiagnostics(ctx, os.Getenv("ACHRIX_IDENTITY_TEST_DSN"), slog.New(slog.NewJSONHandler(&logs, nil)), func(_ context.Context, _ string, phase string, cause error) {
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
