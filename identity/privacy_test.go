// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"log/slog"
	"strings"
	"testing"
)

func TestFailureCanonicalizesPrivateWrappedIdentities(t *testing.T) {
	var logs diagnosticLog
	var protected []error
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Diagnostic: func(_ context.Context, operation, phase string, err error) {
		if phase != "execute" || operation == "" {
			t.Error("diagnostic lost operation/phase")
		}
		protected = append(protected, err)
	}}}
	t.Cleanup(m.flushDiagnostics)
	const private = "private-identity password=hidden host=private-db"
	for _, category := range []error{ErrAuthentication, ErrConflict, ErrLimited} {
		got := m.failure(context.Background(), "query", fmt.Errorf("%s: %w", private, category))
		if got != category || strings.Contains(got.Error(), private) {
			t.Fatalf("expected identity escaped: %v", got)
		}
	}
	if m.FailureCount() != 0 || logs.Len() != 0 {
		t.Fatal("expected domain traffic became an operational failure")
	}
	for _, category := range []error{context.Canceled, context.DeadlineExceeded} {
		got := m.failure(context.Background(), "query", fmt.Errorf("%s: %w", private, category))
		if got != category || strings.Contains(got.Error(), private) {
			t.Fatal("live-caller dependency identity changed")
		}
	}
	if m.FailureCount() != 2 || len(protected) != 2 || !strings.Contains(logs.String(), "dependency_deadline") || !strings.Contains(logs.String(), "dependency_canceled") {
		t.Fatal("live-caller dependency failure lost useful diagnosis")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	before := m.FailureCount()
	_ = m.failure(canceled, "query", fmt.Errorf("%s: %w", private, context.Canceled))
	if m.FailureCount() != before {
		t.Fatal("caller abort became a dependency failure")
	}
	joined := errors.Join(fmt.Errorf("%s: %w", private, context.Canceled), fmt.Errorf("%s: %w", private, ErrUnavailable))
	got := m.failure(context.Background(), "write", joined)
	if !errors.Is(got, context.Canceled) || !errors.Is(got, ErrUnavailable) || strings.Contains(got.Error(), private) {
		t.Fatal("safe joined identities changed")
	}
	provider := &pgconn.PgError{Code: "08006", Message: private}
	got = m.failure(context.Background(), "query", provider)
	var escaped *pgconn.PgError
	var internal *diagnosticError
	if got != ErrUnavailable || errors.As(got, &escaped) || errors.As(got, &internal) {
		t.Fatal("private cause escaped through public chain")
	}
	if protected[len(protected)-1] != provider || !strings.Contains(logs.String(), "sqlstate_08006") || !strings.Contains(logs.String(), `"phase":"execute"`) || strings.Contains(logs.String(), private) {
		t.Fatal("original protected cause or safe structured evidence missing")
	}
	before = m.FailureCount()
	got = m.failure(context.Background(), "query", fmt.Errorf("%s: %w", private, ErrUnavailable))
	if got != ErrUnavailable || m.FailureCount() != before+1 {
		t.Fatal("wrapped unavailability silently discarded")
	}
}
