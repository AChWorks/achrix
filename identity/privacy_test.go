// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFailureCanonicalizesPrivateWrappedIdentities(t *testing.T) {
	var logs bytes.Buffer
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	const private = "private-identity password=hidden host=private-db"

	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrAuthentication, ErrConflict, ErrLimited} {
		got := m.failure(context.Background(), "credential_read", fmt.Errorf("%s: %w", private, category))
		if got != category || strings.Contains(got.Error(), private) {
			t.Fatalf("wrapped expected identity escaped: %v", got)
		}
	}
	if m.FailureCount() != 0 || logs.Len() != 0 {
		t.Fatal("expected identity became operational failure")
	}

	joined := errors.Join(
		fmt.Errorf("%s: %w", private, context.Canceled),
		fmt.Errorf("%s: %w", private, ErrUnavailable),
	)
	got := m.failure(context.Background(), "account_create", joined)
	if !errors.Is(got, context.Canceled) || !errors.Is(got, ErrUnavailable) || strings.Contains(got.Error(), private) {
		t.Fatalf("safe joined identities changed: %v", got)
	}
	if m.FailureCount() != 0 {
		t.Fatal("cancellation ambiguity became a provider diagnostic")
	}

	provider := &pgconn.PgError{Code: "08006", Message: private}
	got = m.failure(context.Background(), "account_read", provider)
	var escaped *pgconn.PgError
	if got != ErrUnavailable || errors.As(got, &escaped) || strings.Contains(got.Error(), private) {
		t.Fatalf("provider object escaped: %v", got)
	}
	if m.FailureCount() != 1 || !strings.Contains(logs.String(), "sqlstate_08006") || strings.Contains(logs.String(), private) {
		t.Fatalf("bounded SQLSTATE diagnostic changed: %s", logs.String())
	}

	got = m.failure(context.Background(), "account_read", fmt.Errorf("%s: %w", private, ErrUnavailable))
	if got != ErrUnavailable || strings.Contains(got.Error(), private) || m.FailureCount() != 2 {
		t.Fatalf("private unavailable wrapper escaped or lost failure accounting: %v", got)
	}
}
