// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type diagnosticLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *diagnosticLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *diagnosticLog) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func (b *diagnosticLog) Len() int       { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.Len() }

type diagnosticContextKey struct{}

func TestDiagnosticsRetainDistinctCausesAndFinalSummary(t *testing.T) {
	var logs diagnosticLog
	var protected []error
	ctx := context.WithValue(context.Background(), diagnosticContextKey{}, "trusted-request")
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Diagnostic: func(got context.Context, operation, phase string, cause error) {
		if got.Value(diagnosticContextKey{}) != "trusted-request" || operation != "query" || phase != "execute" {
			t.Error("lost trusted context or phase")
		}
		protected = append(protected, cause)
	}}}
	t.Cleanup(m.flushDiagnostics)
	one, two := errors.New("first dependency cause password=SECRET_ONE"), errors.New("different dependency cause password=SECRET_TWO")
	_ = m.failure(ctx, "query", one)
	_ = m.failure(ctx, "query", two)
	for i := 0; i < 20; i++ {
		_ = m.failure(ctx, "query", one)
	}
	if len(protected) != 22 || protected[0] != one || protected[1] != two {
		t.Fatal("protected capture missed an original cause before ordinary-log suppression")
	}
	if strings.Count(logs.String(), "\n") != 2 || strings.Contains(logs.String(), "SECRET_") {
		t.Fatal("ordinary logs lost distinct first events or leaked protected causes")
	}
	// No further traffic: the timer must emit the suppressed evidence itself.
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), `"diagnostic_summary":true`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	output := logs.String()
	if !strings.Contains(output, `"suppressed_records":20`) || strings.Contains(output, "SECRET_") {
		t.Fatal("final suppression evidence missing or private", output)
	}
	// A support exporter can select the protected technical cause with targeted
	// redaction; neither public chains nor ordinary logs carry the raw message.
	exported := strings.ReplaceAll(strings.ReplaceAll(protected[0].Error(), "SECRET_ONE", "[redacted]"), "SECRET_TWO", "[redacted]")
	if !strings.Contains(exported, "first dependency cause") || strings.Contains(exported, "SECRET_") {
		t.Fatal("support evidence must retain meaning while redacting the secret")
	}
}
func TestDiagnosticsReserveUnknownOutcomeAndRejectStaleTimer(t *testing.T) {
	var logs diagnosticLog
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	t.Cleanup(m.flushDiagnostics)
	for i := 0; i < 80; i++ {
		m.report(context.Background(), "readiness", "execute", "", fmt.Errorf("failure %d", i))
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := m.failureAt(canceled, "write", "commit", "unknown", context.Canceled); got != context.Canceled {
		t.Fatal("public cancellation identity changed")
	}
	if !strings.Contains(logs.String(), `"phase":"commit"`) || !strings.Contains(logs.String(), `"outcome":"unknown"`) {
		t.Fatal("readiness flood hid uncertain mutation")
	}
	m.diagnostics.mu.Lock()
	generation := m.diagnostics.generation
	m.diagnostics.mu.Unlock()
	m.flushDiagnostics()
	m.report(context.Background(), "query", "execute", "", errors.New("next window"))
	m.diagnostics.flushGeneration(m.logger, generation)
	m.diagnostics.mu.Lock()
	remaining := len(m.diagnostics.entries)
	m.diagnostics.mu.Unlock()
	if remaining != 1 {
		t.Fatal("stale timer flushed a replacement window")
	}
	if !strings.Contains(logs.String(), `"reason":"distinct_failure_overflow"`) {
		t.Fatal("bounded distinct overflow became silent")
	}
}
func TestDiagnosticsConcurrentOwnership(t *testing.T) {
	var logs diagnosticLog
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	defer m.flushDiagnostics()
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 30; j++ {
				_ = m.failure(context.Background(), "query", errors.New("same dependency"))
			}
		}()
	}
	workers.Wait()
	m.flushDiagnostics()
	if m.FailureCount() != 240 || !strings.Contains(logs.String(), `"suppressed_records":239`) {
		t.Fatal("concurrent diagnostics lost accounting")
	}
}

type rollbackFailure struct {
	pgx.Tx
	failure      error
	contextError error
}

func (t *rollbackFailure) Rollback(ctx context.Context) error {
	t.contextError = ctx.Err()
	return t.failure
}
func TestCleanupRetainsPrimaryOutcomeAndIgnoresClosedTransaction(t *testing.T) {
	var logs diagnosticLog
	var phases []string
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Diagnostic: func(_ context.Context, _ string, phase string, _ error) { phases = append(phases, phase) }}}
	defer m.flushDiagnostics()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed := &rollbackFailure{failure: pgx.ErrTxClosed}
	m.rollback(ctx, "write", "committed", closed)
	if m.FailureCount() != 0 {
		t.Fatal("harmless closed cleanup reported as failure")
	}
	broken := &rollbackFailure{failure: &pgconn.PgError{Code: "08006", Message: "SECRET_CLEANUP"}}
	m.rollback(ctx, "write", "committed", broken)
	if broken.contextError != nil || len(phases) != 1 || phases[0] != "rollback" || !strings.Contains(logs.String(), `"outcome":"committed"`) || strings.Contains(logs.String(), "SECRET_CLEANUP") {
		t.Fatal("cleanup lost bounded context, primary success, or secrecy")
	}
}
func TestConfigurationCaptureIsPrivateAndActionable(t *testing.T) {
	var logs diagnosticLog
	var captured error
	_, err := NewPostgres("postgres://user:CONFIG_SECRET@127.0.0.1:99999/db", Config{Diagnostic: func(_ context.Context, operation, phase string, cause error) {
		if operation != "configure" || phase != "database_config" {
			t.Error("wrong configuration phase")
		}
		captured = cause
	}}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if !errors.Is(err, ErrConfiguration) || captured == nil || !strings.Contains(logs.String(), "configuration_parse_failed") || !strings.Contains(err.Error(), "dsn") || strings.Contains(err.Error(), "CONFIG_SECRET") || strings.Contains(logs.String(), "CONFIG_SECRET") {
		t.Fatal("configuration evidence lost or leaked")
	}
	var internal *diagnosticError
	if errors.As(err, &internal) {
		t.Fatal("private configuration unwrap escaped")
	}
}

func TestCommitOutcomeRequiresExplicitProof(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, &pgconn.PgError{Code: "40003"}, &pgconn.PgError{Code: "08007"}, &pgconn.PgError{Code: "40001"}, errors.New("lost acknowledgement")} {
		if commitOutcome(err) != "unknown" {
			t.Fatal("ambiguous commit became replay-safe", err)
		}
	}
	if commitOutcome(fmt.Errorf("rollback: %w", pgx.ErrTxCommitRollback)) != "not_committed" {
		t.Fatal("explicit transaction rollback proof lost")
	}
}
func TestFingerprintBoundsAreVisible(t *testing.T) {
	var logs diagnosticLog
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	defer m.flushDiagnostics()
	_ = m.failure(context.Background(), "query", errors.New(strings.Repeat("x", 4096)))
	if !strings.Contains(logs.String(), `"fingerprint_text_truncated":true`) || !strings.Contains(logs.String(), `"cause_text_bytes":4096`) {
		t.Fatal("bounded fingerprint limitation was hidden")
	}
}

func TestStartupDeadlineRemainsActionable(t *testing.T) {
	var logs diagnosticLog
	var captured error
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Diagnostic: func(_ context.Context, _ string, _ string, cause error) { captured = cause }}}
	defer m.flushDiagnostics()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := m.failureAt(ctx, "start", "check_environment", "not_executed", context.Canceled)
	if got != context.Canceled || captured != context.Canceled || !strings.Contains(logs.String(), `"phase":"check_environment"`) {
		t.Fatal("bounded startup cancellation lost failing phase")
	}
}

func TestAuditInputFieldsAreSafe(t *testing.T) {
	service := &Service{}
	_, err := service.Prepare(context.Background(), "actor", Event{Action: "read", Target: "PRIVATE INVALID TARGET", Authority: "audit.append", Outcome: "succeeded"})
	var input *InputError
	if !errors.Is(err, ErrInput) || !errors.As(err, &input) || input.Field != "target" || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("Audit invalid field lost meaning or echoed input", err)
	}
	_, err = service.Query(context.Background(), "actor", "target", "", 0)
	if !errors.As(err, &input) || input.Field != "limit" {
		t.Fatal("Audit page bound lost field", err)
	}
}

func TestWrappedDistinctDependencyCausesStayVisible(t *testing.T) {
	var logs diagnosticLog
	m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	defer m.flushDiagnostics()
	for _, code := range []string{"08006", "23514"} {
		cause := diagnosticFault("schema_read_failed", "cannot inspect schema", &pgconn.PgError{Code: code, Message: "PRIVATE_SCHEMA_CAUSE"})
		_ = m.failureAt(context.Background(), "start", "check_schema", "not_executed", cause)
	}
	if strings.Count(logs.String(), "\n") != 2 || !strings.Contains(logs.String(), "sqlstate_08006") || !strings.Contains(logs.String(), "sqlstate_23514") || strings.Contains(logs.String(), "PRIVATE_SCHEMA_CAUSE") {
		t.Fatal("safe wrapper collapsed distinct dependency causes", logs.String())
	}
}
