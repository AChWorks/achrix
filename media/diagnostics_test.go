// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type diagnosticBuffer struct {
	mu sync.Mutex
	bytes.Buffer
	summary chan struct{}
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.Buffer.Write(p)
	if bytes.Contains(p, []byte(`"diagnostic_summary":true`)) && b.summary != nil {
		select {
		case b.summary <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func diagnosticModule(t *testing.T) (*Module, *diagnosticBuffer) {
	t.Helper()
	output := &diagnosticBuffer{}
	m := &Module{logger: slog.New(slog.NewJSONHandler(output, nil))}
	t.Cleanup(m.flushDiagnostics)
	return m, output
}

func TestDiagnosticCapturesOriginalCauseAndUsefulSafeDetail(t *testing.T) {
	const secret = "private-credential-token-path"
	for _, test := range []struct {
		name  string
		cause error
		want  []string
	}{
		{"full", &os.PathError{Op: "write", Path: secret, Err: syscall.ENOSPC}, []string{`"reason":"storage_full"`, `"errno":28`}},
		{"permission", &os.PathError{Op: "open", Path: secret, Err: syscall.EACCES}, []string{`"reason":"permission_denied"`, `"errno":13`}},
		{"io", fmt.Errorf("%s: %w", secret, syscall.EIO), []string{`"reason":"storage_io"`, `"errno":5`}},
		{"postgres", &pgconn.PgError{Code: "23514", Message: secret, SchemaName: "media", TableName: "assets", ColumnName: "size"}, []string{`"sqlstate":"23514"`, `"table":"assets"`, `"column":"size"`}},
		{"short", fmt.Errorf("%s: %w", secret, io.ErrShortWrite), []string{`"reason":"short_write"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, log := diagnosticModule(t)
			var captured error
			m.config.Diagnostic = func(_ context.Context, operation, phase string, cause error) {
				if operation != "prepare_output" || phase != "prepare_output" {
					t.Error("lost phase", operation, phase)
				}
				captured = cause
			}
			public := m.preparationFailure(context.Background(), "prepare_output", test.cause)
			if captured != test.cause || public != ErrUnavailable {
				t.Fatal("cause/outward boundary", captured, public)
			}
			var leaked *os.PathError
			var pg *pgconn.PgError
			if errors.As(public, &leaked) || errors.As(public, &pg) || strings.Contains(public.Error(), secret) {
				t.Fatal("private public chain")
			}
			for _, detail := range append(test.want, `"phase":"prepare_output"`, `"component":"achrix.media"`) {
				if !strings.Contains(log.String(), detail) {
					t.Fatalf("missing %s in %s", detail, log.String())
				}
			}
			if strings.Contains(log.String(), secret) {
				t.Fatal("ordinary diagnostic retained secret")
			}
		})
	}
}

func TestPublicImageDestinationRetainsTypedCauseBeforeMapping(t *testing.T) {
	m, log := diagnosticModule(t)
	var captured error
	m.config.Diagnostic = func(_ context.Context, _, _ string, cause error) { captured = cause }
	profile, err := inspectPublicImage(context.Background(), imageBytes(t, "png", 2, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cause := &os.PathError{Op: "write", Path: "/private-output", Err: syscall.ENOSPC}
	_, _, _, _, err = encodePublicImage(context.Background(), imageBytes(t, "png", 2, 2), profile, wrappedPublicWriter{cause})
	if err != cause {
		t.Fatal("encoder destroyed original destination failure", err)
	}
	if public := m.preparationFailure(context.Background(), "prepare_output", err); public != ErrUnavailable || captured != cause {
		t.Fatal("preparation diagnostic boundary", public, captured)
	}
	if !strings.Contains(log.String(), `"reason":"storage_full"`) || strings.Contains(log.String(), "/private-output") {
		t.Fatal(log.String())
	}
	if public := m.preparationFailure(context.Background(), "prepare_output", ErrUnknownOutcome); public != ErrUnavailable {
		t.Fatal("destination failure invented Media mutation", public)
	}
}

func TestDependencyCancellationIsDiagnosedWithLiveCaller(t *testing.T) {
	m, log := diagnosticModule(t)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := m.failure(context.Background(), "read_lock", fmt.Errorf("private connection: %w", cause)); got != cause {
			t.Fatal(got)
		}
	}
	if m.FailureCount() != 2 || !strings.Contains(log.String(), "dependency_canceled") || !strings.Contains(log.String(), "dependency_deadline") {
		t.Fatal("live dependency failure hidden", log.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := m.failure(ctx, "read_copy", context.Canceled); got != context.Canceled || m.FailureCount() != 2 {
		t.Fatal("caller cancellation not distinct")
	}
	if got := m.failure(ctx, "create_publish", errors.Join(ErrUnknownOutcome, context.Canceled)); !errors.Is(got, ErrUnknownOutcome) || m.FailureCount() != 3 {
		t.Fatal("unknown mutation hidden by caller cancellation")
	}
}

func TestDiagnosticBudgetRetainsDistinctKindsAndUnknownOutcomes(t *testing.T) {
	m, log := diagnosticModule(t)
	for range 20 {
		_ = m.failure(context.Background(), "read_storage", syscall.ENOSPC)
	}
	_ = m.failure(context.Background(), "read_storage", syscall.EACCES)
	for i := 0; i < maxDiagnosticKinds; i++ {
		_ = m.failure(context.Background(), fmt.Sprintf("readiness_%d", i), syscall.EIO)
	}
	_ = m.failure(context.Background(), "create_publish", errors.Join(ErrUnknownOutcome, context.DeadlineExceeded))
	if !strings.Contains(log.String(), "storage_full") || !strings.Contains(log.String(), "permission_denied") || !strings.Contains(log.String(), `"outcome":"unknown"`) {
		t.Fatal("distinct or unknown failure hidden")
	}
	m.diagnostics.mu.Lock()
	if len(m.diagnostics.entries) > maxDiagnosticKinds {
		t.Error("unbounded diagnostic keys")
	}
	m.diagnostics.mu.Unlock()
	m.flushDiagnostics()
	for _, detail := range []string{`"suppressed_records":19`, `"diagnostic_summary":true`, `"truncated":true`} {
		if !strings.Contains(log.String(), detail) {
			t.Fatal("missing bounded suppression evidence", detail)
		}
	}
}

func TestDiagnosticFinalSummaryDoesNotNeedMoreTraffic(t *testing.T) {
	m, log := diagnosticModule(t)
	log.summary = make(chan struct{}, 1)
	_ = m.failure(context.Background(), "read_storage", syscall.ENOSPC)
	_ = m.failure(context.Background(), "read_storage", syscall.ENOSPC)
	select {
	case <-log.summary:
	case <-time.After(3 * time.Second):
		t.Fatal("suppressed evidence disappeared when traffic stopped")
	}
	if !strings.Contains(log.String(), `"suppressed_records":1`) {
		t.Fatal(log.String())
	}
}

func TestStaleDiagnosticTimerCannotClearNextWindow(t *testing.T) {
	m, _ := diagnosticModule(t)
	_ = m.failure(context.Background(), "read_storage", syscall.ENOSPC)
	m.diagnostics.mu.Lock()
	old := m.diagnostics.generation
	m.diagnostics.mu.Unlock()
	m.flushDiagnostics()
	_ = m.failure(context.Background(), "read_storage", syscall.EACCES)
	m.flushDiagnosticWindow(old)
	m.diagnostics.mu.Lock()
	defer m.diagnostics.mu.Unlock()
	if len(m.diagnostics.entries) != 1 {
		t.Fatal("old fired timer discarded new diagnostic window")
	}
}

func TestCleanupPreservesMeaningfulJoinedFailure(t *testing.T) {
	m, log := diagnosticModule(t)
	m.cleanupFailure(context.Background(), "create", "asset_unlock", pgx.ErrTxClosed)
	m.cleanupFailure(context.Background(), "create", "source_close", fmt.Errorf("already closed: %w", os.ErrClosed))
	if m.FailureCount() != 0 {
		t.Fatal("expected cleanup became incident")
	}
	m.cleanupFailure(context.Background(), "create", "source_close", errors.Join(os.ErrClosed, syscall.EIO))
	if m.FailureCount() != 1 || !strings.Contains(log.String(), `"outcome":"primary_unchanged"`) || !strings.Contains(log.String(), `"reason":"storage_io"`) {
		t.Fatal("meaningful cleanup lost", log.String())
	}
}

func TestMigrationDiagnosticsPreserveConfigurationCausePrivately(t *testing.T) {
	var log bytes.Buffer
	var captured error
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := MigrateWithDiagnostics(ctx, "postgres://private-credential@localhost/%zz", slog.New(slog.NewJSONHandler(&log, nil)), func(_ context.Context, operation, phase string, cause error) {
		if operation != "migrate" || phase != "connection_config" {
			t.Error("migration phase", operation, phase)
		}
		captured = cause
	})
	if !errors.Is(err, ErrConfiguration) || captured == nil {
		t.Fatal("migration diagnostics absent", err)
	}
	if strings.Contains(err.Error()+log.String(), "private-credential") || !strings.Contains(log.String(), "dsn_syntax") || !strings.Contains(log.String(), `"outcome":"not_executed"`) {
		t.Fatal("configuration diagnostics not useful and safe", err, log.String())
	}
}

func TestConstructorDistinguishesSafeDatabaseRequirements(t *testing.T) {
	const private = "private-dsn-marker"
	for _, scenario := range []struct {
		name, dsn, reason string
		want              []string
	}{
		{"empty", "", "dsn_length", []string{"DSN", "between 1 and 4096 bytes"}},
		{"oversized", strings.Repeat(private, 300), "dsn_length", []string{"DSN", "between 1 and 4096 bytes"}},
		{"syntax", "host=localhost port=" + private + " sslmode=disable", "dsn_syntax", []string{"DSN syntax is invalid", "PostgreSQL URI", "keyword=value"}},
		{"remote_tls", "host=" + private + ".example sslmode=disable", "verified_tls_required", []string{"database host requires verified TLS", "sslmode=verify-full"}},
		{"fallback_tls", "host=localhost," + private + ".example sslmode=disable", "fallback_verified_tls_required", []string{"database fallback host requires verified TLS", "sslmode=verify-full"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var captured error
			calls := 0
			m, err := NewPostgres(scenario.dsn, Config{StorageRoot: "/unused", Diagnostic: func(_ context.Context, operation, phase string, cause error) {
				if operation != "configure" || phase != "database" {
					t.Error("configuration diagnostic ownership lost", operation, phase)
				}
				captured = cause
				calls++
			}}, nil)
			if m != nil || !errors.Is(err, ErrConfiguration) || errors.Unwrap(err) != ErrConfiguration {
				t.Fatal("constructor result did not retain only its safe identity", err)
			}
			for _, detail := range scenario.want {
				if !strings.Contains(err.Error(), detail) {
					t.Fatalf("missing actionable requirement %q in %v", detail, err)
				}
			}
			var diagnostic *diagnosticFault
			if calls != 1 || !errors.As(captured, &diagnostic) || diagnostic.reason != scenario.reason {
				t.Fatal("protected configuration cause missing", calls, captured)
			}
			if scenario.reason == "dsn_syntax" && !strings.Contains(captured.Error(), private) {
				t.Fatal("original parser evidence destroyed", captured)
			}
			if errors.As(err, &diagnostic) || strings.Contains(err.Error(), private) {
				t.Fatal("private configuration cause escaped", err)
			}
		})
	}
}

func TestStorageStartupDistinguishesMissingRootAndWrongMode(t *testing.T) {
	for _, wrongMode := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongMode), func(t *testing.T) {
			root := t.TempDir()
			want := "storage_root_unavailable"
			if wrongMode {
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
				want = "storage_root_mode_0700_required"
			} else {
				root += "/private-missing-root"
			}
			var log bytes.Buffer
			m, err := NewPostgres("host=localhost sslmode=disable", Config{StorageRoot: root}, slog.New(slog.NewJSONHandler(&log, nil)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if got := m.Start(ctx); !errors.Is(got, ErrUnavailable) {
				t.Fatal("unexpected startup result", got)
			}
			if err := m.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(log.String(), want) || strings.Contains(log.String(), root) {
				t.Fatal("storage prerequisite diagnosis", log.String())
			}
		})
	}
}

func TestSafeInputErrorCarriesRuleWithoutSubmittedValue(t *testing.T) {
	s := &Service{}
	_, err := s.Create(context.Background(), "", "private/filename", bytes.NewReader(nil))
	var input *InputError
	if !errors.Is(err, ErrInput) || !errors.As(err, &input) || input.Field() != "filename" || input.Reason() != "invalid_filename" || strings.Contains(err.Error(), "private") {
		t.Fatal("input details missing or unsafe", err)
	}
	wrapped := fmt.Errorf("private provider detail: %w", err)
	public := canonicalFailureError(wrapped)
	if !errors.As(public, &input) || input.Field() != "filename" || strings.Contains(public.Error(), "private") {
		t.Fatal("source-owned field lost or private wrapper escaped", public)
	}
}

func TestMutationSQLStateDoesNotInventNonExecution(t *testing.T) {
	for _, code := range []string{"23514", "40003", "08007", "08006"} {
		m, log := diagnosticModule(t)
		cause := &pgconn.PgError{Severity: "ERROR", Code: code, Message: "private-driver-message"}
		result := m.failure(context.Background(), "create_publish", mutationError(cause))
		unknown := code != "23514"
		if errors.Is(result, ErrUnknownOutcome) != unknown {
			t.Fatal("incorrect mutation certainty", code, result)
		}
		outcome := "not_executed"
		if unknown {
			outcome = "unknown"
		}
		if !strings.Contains(log.String(), `"outcome":"`+outcome+`"`) || !strings.Contains(log.String(), `"sqlstate":"`+code+`"`) || strings.Contains(log.String()+result.Error(), "private-driver-message") {
			t.Fatal("certainty or cause evidence lost", code, log.String(), result)
		}
		m.flushDiagnostics()
	}
}

func TestDistinctGenericCausesAndProtectedHookAreNotSilenced(t *testing.T) {
	m, log := diagnosticModule(t)
	calls := 0
	m.config.Diagnostic = func(context.Context, string, string, error) { calls++ }
	for _, cause := range []error{errors.New("private-first-cause"), errors.New("private-second-cause"), errors.New("private-first-cause")} {
		_ = m.failure(context.Background(), "read_storage", cause)
	}
	if calls != 3 || strings.Count(log.String(), "media operation failed") != 2 || strings.Contains(log.String(), "private-first") || strings.Contains(log.String(), "private-second") {
		t.Fatal("distinct causes or protected hook suppressed", calls, log.String())
	}
	m.flushDiagnostics()
	if !strings.Contains(log.String(), `"suppressed_records":1`) || !strings.Contains(log.String(), `"cause_length":19`) {
		t.Fatal("final useful summary absent", log.String())
	}
}

type diagnosticContextKey struct{}
type diagnosticContextHandler struct {
	slog.Handler
	t     *testing.T
	first *int
}

func (h diagnosticContextHandler) Handle(ctx context.Context, record slog.Record) error {
	summary := false
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "diagnostic_summary" {
			summary = a.Value.Bool()
		}
		return true
	})
	if summary && ctx.Value(diagnosticContextKey{}) != nil {
		h.t.Error("aggregate summary retained one request context")
	}
	if !summary && ctx.Value(diagnosticContextKey{}) == "trusted-request" {
		*h.first = *h.first + 1
	}
	return h.Handler.Handle(ctx, record)
}

func TestDiagnosticSummaryDropsSingularRequestCorrelation(t *testing.T) {
	var output bytes.Buffer
	first := 0
	m := &Module{logger: slog.New(diagnosticContextHandler{slog.NewJSONHandler(&output, nil), t, &first})}
	t.Cleanup(m.flushDiagnostics)
	ctx := context.WithValue(context.Background(), diagnosticContextKey{}, "trusted-request")
	_ = m.failure(ctx, "read_storage", syscall.ENOSPC)
	_ = m.failure(ctx, "read_storage", syscall.ENOSPC)
	m.flushDiagnostics()
	if first != 1 || !strings.Contains(output.String(), `"diagnostic_summary":true`) {
		t.Fatal("first correlation or final summary missing", first, output.String())
	}
}

func TestDiagnosticFingerprintTruncationIsExplicit(t *testing.T) {
	m, log := diagnosticModule(t)
	_ = m.failure(context.Background(), "read_storage", errors.New(strings.Repeat("x", 5000)))
	if !strings.Contains(log.String(), `"cause_length":5000`) || !strings.Contains(log.String(), `"cause_sample_truncated":true`) || strings.Contains(log.String(), strings.Repeat("x", 20)) {
		t.Fatal("private sample leaked or truncation hidden", log.String())
	}
}

func TestExpectedSentinelCannotHideJoinedOperationalCause(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		cause  error
		reason string
	}{
		{"io", &os.PathError{Op: "read", Path: "private-joined-path", Err: syscall.EIO}, "storage_io"},
		{"dependency_deadline", fmt.Errorf("private-joined-dependency: %w", context.DeadlineExceeded), "dependency_deadline"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m, log := diagnosticModule(t)
			original := errors.Join(ErrInput, scenario.cause)
			var captured error
			m.config.Diagnostic = func(_ context.Context, operation, phase string, cause error) {
				if operation != "create_input" || phase != "create_input" {
					t.Error("joined fault owner/phase lost", operation, phase)
				}
				captured = cause
			}
			public := m.failure(context.Background(), "create_input", original)
			if !errors.Is(public, ErrInput) || errors.Is(public, context.DeadlineExceeded) != errors.Is(scenario.cause, context.DeadlineExceeded) {
				t.Fatal("safe public identities changed", public)
			}
			var leaked *os.PathError
			if errors.As(public, &leaked) || errors.Is(public, syscall.EIO) || strings.Contains(public.Error(), "private-joined") {
				t.Fatal("private joined object escaped", public)
			}
			if captured != original || m.FailureCount() != 1 || !strings.Contains(log.String(), `"reason":"`+scenario.reason+`"`) || strings.Contains(log.String(), "private-joined") {
				t.Fatal("expected sentinel hid actual operational evidence", captured, m.FailureCount(), log.String())
			}
			if scenario.name == "io" && !strings.Contains(log.String(), `"errno":5`) {
				t.Fatal("joined errno evidence missing", log.String())
			}
		})
	}
}
