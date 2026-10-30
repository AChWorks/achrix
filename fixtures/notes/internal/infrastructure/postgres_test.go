// SPDX-License-Identifier: MPL-2.0
package infrastructure

import (
	"context"
	"errors"
	"example.com/achrix-notes/internal/domain"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFailureReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		reason string
	}{
		{"driver", &pgconn.PgError{Code: "22012", Message: "division by zero"}, "22012"},
		{"deadline", context.DeadlineExceeded, "deadline"},
		{"cancellation", context.Canceled, "canceled"},
		{"connection", errors.New("database unreachable"), "connection_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("operation failed: %w", tc.cause)
			if got := FailureReason(err); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestWriteOutcomeRequiresEvidenceBeyondTimeoutOrErrorType(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		outcome string
		public  error
	}{
		{context.DeadlineExceeded, "unknown", domain.ErrUnknownOutcome},
		{context.Canceled, "unknown", domain.ErrUnknownOutcome},
		{&pgconn.PgError{Code: "40003", Message: "PRIVATE_SQL"}, "unknown", domain.ErrUnknownOutcome},
		{&pgconn.PgError{Code: "08007", Message: "PRIVATE_SQL"}, "unknown", domain.ErrUnknownOutcome},
		{&pgconn.PgError{Code: "42P01", Message: "PRIVATE_SQL"}, "not_committed", domain.ErrNotCommitted},
	} {
		outcome, outward := writeOutcome(fmt.Errorf("PRIVATE_PROVIDER: %w", tc.cause))
		if outcome != tc.outcome || outward != tc.public || !errors.Is(outward, domain.ErrUnavailable) || strings.Contains(outward.Error(), "PRIVATE_") {
			t.Fatal(outcome, outward)
		}
		var leaked *pgconn.PgError
		if errors.As(outward, &leaked) {
			t.Fatal("driver cause escaped canonical result")
		}
	}
}

func TestConfigurationCauseIsAvailableOnlyAtProtectedBoundary(t *testing.T) {
	var original error
	_, outward := NewWithDiagnostics("host=localhost password=PRIVATE_DSN port=not_a_number", nil, func(_ context.Context, operation, phase string, cause error) {
		if operation != "configure" || phase != "database_config" {
			t.Fatal(operation, phase)
		}
		original = cause
	})
	if outward == nil || original == nil || !strings.Contains(outward.Error(), "NOTES_DATABASE_URL") || strings.Contains(outward.Error(), "PRIVATE_DSN") {
		t.Fatal("lost safe configuration detail or original cause")
	}
	var parser *pgconn.ParseConfigError
	if !errors.As(original, &parser) || errors.As(outward, &parser) {
		t.Fatal("parse diagnostic boundary incorrect")
	}
}
