// SPDX-License-Identifier: MPL-2.0
package infrastructure

import (
	"context"
	"errors"
	"fmt"
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
