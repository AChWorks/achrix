// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// diagnosticError is private to the diagnostic boundary. Its explanation and
// fields are source-owned; its cause is never returned through public errors.
type diagnosticError struct {
	reason string
	detail string
	fields []slog.Attr
	cause  error
}

func (e *diagnosticError) Error() string { return e.detail }
func (e *diagnosticError) Unwrap() error { return e.cause }
func diagnosticFault(reason, detail string, cause error, fields ...slog.Attr) error {
	return &diagnosticError{reason: reason, detail: detail, cause: cause, fields: fields}
}
func configuration(field, requirement string) error {
	return fmt.Errorf("%w: %s %s", ErrConfiguration, field, requirement)
}

// Classify before public canonicalization. Arbitrary provider messages, SQL,
// DSNs and payloads are only available to the explicitly protected hook.
func diagnosticDetails(err error) (string, []slog.Attr) {
	attrs := []slog.Attr{slog.String("cause_type", fmt.Sprintf("%T", err))}
	var owned *diagnosticError
	if errors.As(err, &owned) {
		attrs = append(attrs, slog.String("detail", owned.detail))
		attrs = append(attrs, owned.fields...)
		if owned.cause != nil {
			reason, nested := diagnosticDetails(owned.cause)
			attrs = append(attrs, slog.String("cause_reason", reason))
			attrs = append(attrs, slog.Group("dependency", attrsToAny(nested)...))
		}
		return owned.reason, attrs
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && validSQLState(pe.Code) {
		return "sqlstate_" + pe.Code, append(attrs, slog.String("sqlstate", pe.Code))
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "dependency_deadline", attrs
	case errors.Is(err, context.Canceled):
		return "dependency_canceled", attrs
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused", attrs
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset", attrs
	case errors.Is(err, syscall.EPIPE):
		return "connection_broken_pipe", attrs
	case errors.Is(err, syscall.ENOSPC):
		return "storage_full", attrs
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "permission_denied", attrs
	case errors.Is(err, syscall.EIO):
		return "storage_io", attrs
	}
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknownAuthority) || errors.As(err, &certificateInvalid) || errors.As(err, &hostname) {
		return "tls_verification_failed", attrs
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns_failure", attrs
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "dependency_timeout", attrs
	}
	var connection *pgconn.ConnectError
	if errors.As(err, &connection) {
		return "connection_failed", attrs
	}
	return "dependency_failure", attrs
}
func validSQLState(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, c := range code {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}

// Fixed memory and one timer per component. Reserve eight of 64 signatures for
// uncertain mutations so a readiness burst cannot consume their first evidence.
// Fingerprints retain no raw cause text and never leave this private window.
type diagnosticKey struct {
	operation, phase, outcome, reason string
	fingerprint                       [32]byte
}
type diagnosticRepeat struct {
	attrs []slog.Attr
	count uint64
}
type diagnosticBudget struct {
	mu                          sync.Mutex
	generation                  uint64
	timer                       *time.Timer
	entries                     map[diagnosticKey]*diagnosticRepeat
	normal, uncertain           int
	overflow, uncertainOverflow uint64
}

func diagnosticFingerprint(err error) ([32]byte, int, bool) {
	cause := err
	// Our safe wrapper text is intentionally not the original explanation.
	// Include the underlying cause so independent wrapped incidents remain
	// distinct; the bounded fingerprint is private and retains no raw text.
	for depth := 0; depth < 8; depth++ {
		owned, ok := cause.(*diagnosticError)
		if !ok || owned.cause == nil {
			break
		}
		cause = owned.cause
	}
	text := cause.Error()
	size := len(text)
	if size > 2048 {
		text = text[:2048]
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%T:%d:%s", err, size, text))), size, size > 2048
}
func (b *diagnosticBudget) emit(ctx context.Context, logger *slog.Logger, hook func(context.Context, string, string, error), operation, phase, outcome string, err error) {
	// Protected capture precedes ordinary-log suppression. The product owns
	// provider-aware redaction and bounded retention/export of this callback.
	if hook != nil {
		hook(ctx, operation, phase, err)
	}
	reason, details := diagnosticDetails(err)
	attrs := []slog.Attr{slog.String("component", "achrix.audit"), slog.String("operation", operation), slog.String("phase", phase), slog.String("reason", reason)}
	if outcome != "" {
		attrs = append(attrs, slog.String("outcome", outcome))
	}
	attrs = append(attrs, details...)
	fingerprint, causeBytes, truncated := diagnosticFingerprint(err)
	if truncated {
		attrs = append(attrs, slog.Bool("fingerprint_text_truncated", true), slog.Int("cause_text_bytes", causeBytes))
	}
	key := diagnosticKey{operation, phase, outcome, reason, fingerprint}
	b.mu.Lock()
	if b.entries == nil {
		b.entries = make(map[diagnosticKey]*diagnosticRepeat)
		b.generation++
		generation := b.generation
		b.timer = time.AfterFunc(time.Second, func() { b.flushGeneration(logger, generation) })
	}
	if previous := b.entries[key]; previous != nil {
		if previous.count < ^uint64(0) {
			previous.count++
		}
		b.mu.Unlock()
		return
	}
	uncertain := outcome == "unknown"
	full := !uncertain && b.normal >= 56 || uncertain && b.uncertain >= 8
	if full {
		counter := &b.overflow
		if uncertain {
			counter = &b.uncertainOverflow
		}
		if *counter < ^uint64(0) {
			*counter++
		}
		b.mu.Unlock()
		return
	}
	b.entries[key] = &diagnosticRepeat{attrs: attrs}
	if uncertain {
		b.uncertain++
	} else {
		b.normal++
	}
	b.mu.Unlock()
	logger.LogAttrs(ctx, slog.LevelError, "audit operation failed", attrs...)
}
func (b *diagnosticBudget) flush(logger *slog.Logger) { b.flushGeneration(logger, 0) }
func (b *diagnosticBudget) flushGeneration(logger *slog.Logger, generation uint64) {
	b.mu.Lock()
	if generation != 0 && generation != b.generation {
		b.mu.Unlock()
		return
	}
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	entries, overflow, uncertainOverflow := b.entries, b.overflow, b.uncertainOverflow
	b.entries, b.normal, b.uncertain, b.overflow, b.uncertainOverflow = nil, 0, 0, 0, 0
	b.generation++
	b.mu.Unlock()
	// Counts cover multiple calls. Never assign them a request's context/ID.
	for _, entry := range entries {
		if entry.count == 0 {
			continue
		}
		attrs := append(append([]slog.Attr(nil), entry.attrs...), slog.Bool("diagnostic_summary", true), slog.Uint64("suppressed_records", entry.count))
		logger.LogAttrs(context.Background(), slog.LevelError, "audit repeated failures", attrs...)
	}
	if overflow > 0 || uncertainOverflow > 0 {
		logger.Error("audit diagnostic capacity exceeded", "component", "achrix.audit", "diagnostic_summary", true, "reason", "distinct_failure_overflow", "suppressed_records", overflow, "unknown_outcome_records", uncertainOverflow, "distinct_limit", 64)
	}
}
func (m *Module) report(ctx context.Context, operation, phase, outcome string, err error) {
	m.failures.Add(1)
	logger := m.logger
	if logger == nil {
		logger = slog.Default()
	}
	m.diagnostics.emit(ctx, logger, m.config.Diagnostic, operation, phase, outcome, err)
}

func safeConfigurationDetail(err error) string {
	var owned *diagnosticError
	if errors.As(err, &owned) {
		return owned.detail
	}
	// Configuration errors here are generated from source-owned field rules.
	return err.Error()
}
func constructorDiagnostic(config Config, logger *slog.Logger, phase string, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Module{config: config, logger: logger}
	m.report(context.Background(), "configure", phase, "not_executed", err)
	m.diagnostics.flush(logger)
}

func (m *Module) flushDiagnostics() {
	logger := m.logger
	if logger == nil {
		logger = slog.Default()
	}
	m.diagnostics.flush(logger)
}

func (m *Module) rollback(ctx context.Context, operation, outcome string, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		m.report(cleanup, operation, "rollback", outcome, err)
	}
}

func unavailable(detail string) error { return fmt.Errorf("%w: %s", ErrUnavailable, detail) }
func commitOutcome(err error) string {
	// Statement/transaction-resolution-unknown SQLSTATEs and transport failures
	// are not rollback proof. Only the driver's explicit rollback result is.
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return "not_committed"
	}
	return "unknown"
}
