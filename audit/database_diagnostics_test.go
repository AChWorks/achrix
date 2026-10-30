// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCheckDatabaseConfigurationDetailsAndProtectedCause(t *testing.T) {
	const configured = "host=127.0.0.1 port=5432 user=fixture password=AUDIT_CHECK_SECRET dbname=fixture sslmode=disable"
	for _, tc := range []struct {
		name, dsn, detail, phase, reason string
		parseCause                       bool
	}{
		{"malformed DSN", "postgres://fixture:AUDIT_CHECK_SECRET@127.0.0.1:99999/fixture", "dsn must be a valid PostgreSQL connection configuration", "database_config", "configuration_parse_failed", true},
		{"unsafe TLS", "host=AUDIT_CHECK_HOST user=fixture password=AUDIT_CHECK_SECRET dbname=fixture sslmode=disable", "dsn.tls requires certificate and hostname verification for remote hosts", "database_config", "configuration_precondition_failed", false},
		{"different database", "host=127.0.0.1 port=5432 user=fixture password=AUDIT_CHECK_SECRET dbname=AUDIT_CHECK_DATABASE sslmode=disable", "dsn must match Audit's database and security profile", "compare_profile", "configuration_profile_mismatch", false},
		{"different credentials", "host=127.0.0.1 port=5432 user=fixture password=AUDIT_CHECK_OTHER_SECRET dbname=fixture sslmode=disable", "dsn must match Audit's database and security profile", "compare_profile", "configuration_profile_mismatch", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs diagnosticLog
			var protected error
			m, err := NewPostgres(configured, Config{Diagnostic: func(ctx context.Context, operation, phase string, cause error) {
				if ctx == nil || operation != "check_database" || phase != tc.phase {
					t.Error("configuration comparison lost operation or phase")
				}
				protected = cause
			}}, slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.cancel(); m.flushDiagnostics() })
			s := &Service{module: m}
			if err = s.CheckDatabase("postgres://fixture:AUDIT_CHECK_SECRET@127.0.0.1:5432/fixture?sslmode=disable"); err != nil || protected != nil || logs.Len() != 0 {
				t.Fatal("equivalent profile became a failure")
			}
			err = s.CheckDatabase(tc.dsn)
			if !errors.Is(err, ErrConfiguration) || !strings.Contains(err.Error(), tc.detail) || protected == nil || !strings.Contains(logs.String(), tc.reason) || !strings.Contains(logs.String(), `"phase":"`+tc.phase+`"`) || !strings.Contains(logs.String(), `"outcome":"not_executed"`) {
				t.Fatal("configuration comparison lost useful safe requirements or protected cause", err, logs.String())
			}
			var parse *pgconn.ParseConfigError
			if errors.As(protected, &parse) != tc.parseCause {
				t.Fatal("original PostgreSQL parse cause was discarded or invented")
			}
			// pgconn's invalid-port ParseConfigError is a terminal cause: its
			// Unwrap may be nil. The protected object must still retain the exact
			// input and useful driver explanation; neither belongs in public output.
			if tc.parseCause && (parse.ConnString != tc.dsn || !strings.Contains(parse.Error(), "invalid port")) {
				t.Fatal("protected parse cause lost its original input or driver explanation")
			}
			var escaped *pgconn.ParseConfigError
			var internal *diagnosticError
			if errors.As(err, &escaped) || errors.As(err, &internal) || strings.Contains(err.Error()+logs.String(), "AUDIT_CHECK_") {
				t.Fatal("configuration comparison exposed a private parse cause or value")
			}
		})
	}
}
