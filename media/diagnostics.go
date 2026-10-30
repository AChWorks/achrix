// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// These errors remain private until diagnosis; no public failure contains their
// raw cause. Fields here are source-owned facts, never configuration or payloads.
type diagnosticFault struct {
	phase, reason string
	outcome       string
	cause         error
	fields        []slog.Attr
}

func (e *diagnosticFault) Error() string {
	if e.cause == nil {
		return e.reason
	}
	return e.reason + ": " + e.cause.Error()
}
func (e *diagnosticFault) Unwrap() error { return e.cause }
func fault(phase, reason string, cause error, fields ...slog.Attr) error {
	return &diagnosticFault{phase: phase, reason: reason, cause: cause, fields: fields}
}

// Inspect every joined member before deciding that a failure is routine.
// Wrapping an expected identity remains quiet; adding an operational sibling
// does not. Public mapping still exposes only the supported safe identities.
func hasOperationalMember(ctx context.Context, cause error) bool {
	if cause == nil {
		return false
	}
	if joined, ok := cause.(interface{ Unwrap() []error }); ok {
		for _, member := range joined.Unwrap() {
			if hasOperationalMember(ctx, member) {
				return true
			}
		}
		return false
	}
	if wrapped, ok := cause.(interface{ Unwrap() error }); ok && wrapped.Unwrap() != nil {
		return hasOperationalMember(ctx, wrapped.Unwrap())
	}
	for _, expected := range []error{ErrInput, ErrConflict, ErrLimited, ErrNotFound} {
		if errors.Is(cause, expected) {
			return false
		}
	}
	return ctx.Err() == nil || !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded)
}

type diagnosticEntry struct {
	fields     []slog.Attr
	suppressed uint64
}

type diagnosticBudget struct {
	mu              sync.Mutex
	entries         map[string]*diagnosticEntry
	timer           *time.Timer
	generation      uint64
	unknownKinds    int
	overflow        uint64
	unknownOverflow uint64
}

// Each slot represents a source-owned operation/phase and typed cause, never an
// asset, request, private message or arbitrary diagnostic field. The cap also
// protects against accidental future caller-controlled operation labels.
const (
	maxDiagnosticKinds   = 128
	reservedUnknownKinds = 16
)

func (m *Module) diagnose(ctx context.Context, operation, phase string, cause error, outcome string) {
	var f *diagnosticFault
	if errors.As(cause, &f) {
		phase = f.phase
	}
	if m.config.Diagnostic != nil {
		m.config.Diagnostic(ctx, operation, phase, cause)
	}
	fields, key := diagnosticFields(ctx, operation, phase, cause, outcome)
	m.diagnostics.mu.Lock()
	if m.diagnostics.entries == nil {
		m.diagnostics.entries = make(map[string]*diagnosticEntry)
	}
	if entry := m.diagnostics.entries[key]; entry != nil {
		if entry.suppressed != ^uint64(0) {
			entry.suppressed++
		}
		m.diagnostics.mu.Unlock()
		return
	}
	unknown := outcome == "unknown"
	if len(m.diagnostics.entries) >= maxDiagnosticKinds || !unknown && len(m.diagnostics.entries)-m.diagnostics.unknownKinds >= maxDiagnosticKinds-reservedUnknownKinds {
		counter := &m.diagnostics.overflow
		if unknown {
			counter = &m.diagnostics.unknownOverflow
		}
		first := *counter == 0
		if *counter != ^uint64(0) {
			*counter = *counter + 1
		}
		m.diagnostics.mu.Unlock()
		if first {
			m.logDiagnostic(ctx, append(fields, slog.Bool("diagnostic_summary", true), slog.Bool("sampled", true), slog.Bool("truncated", true)))
		}
		return
	}
	m.diagnostics.entries[key] = &diagnosticEntry{fields: fields}
	if unknown {
		m.diagnostics.unknownKinds++
	}
	if m.diagnostics.timer == nil {
		m.diagnostics.generation++
		generation := m.diagnostics.generation
		m.diagnostics.timer = time.AfterFunc(time.Second, func() { m.flushDiagnosticWindow(generation) })
	}
	m.diagnostics.mu.Unlock()
	m.logDiagnostic(ctx, fields)
}

func (m *Module) logDiagnostic(ctx context.Context, fields []slog.Attr) {
	logger := m.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(ctx, slog.LevelError, "media operation failed", fields...)
}

// Timer flush guarantees that suppressed evidence does not depend on another
// request arriving. Stop and standalone migration also flush before returning.
// Summaries aggregate requests and deliberately carry no request context/ID.
func (m *Module) flushDiagnostics() {
	m.flushDiagnosticWindow(0)
}

func (m *Module) flushDiagnosticWindow(generation uint64) {
	m.diagnostics.mu.Lock()
	if generation != 0 && generation != m.diagnostics.generation {
		m.diagnostics.mu.Unlock()
		return
	}
	entries, overflow, unknownOverflow := m.diagnostics.entries, m.diagnostics.overflow, m.diagnostics.unknownOverflow
	m.diagnostics.entries, m.diagnostics.overflow = nil, 0
	m.diagnostics.unknownKinds, m.diagnostics.unknownOverflow = 0, 0
	// A callback already admitted by the runtime cannot flush a later window.
	m.diagnostics.generation++
	if m.diagnostics.timer != nil {
		m.diagnostics.timer.Stop()
		m.diagnostics.timer = nil
	}
	m.diagnostics.mu.Unlock()
	for _, entry := range entries {
		if entry.suppressed == 0 {
			continue
		}
		fields := append(append([]slog.Attr(nil), entry.fields...), slog.Uint64("suppressed_records", entry.suppressed), slog.Bool("diagnostic_summary", true))
		m.logDiagnostic(context.Background(), fields)
	}
	if overflow != 0 {
		m.logDiagnostic(context.Background(), []slog.Attr{slog.String("component", "achrix.media"), slog.String("operation", "diagnostics"), slog.String("phase", "capacity"), slog.String("reason", "distinct_kind_limit"), slog.Uint64("suppressed_records", overflow), slog.Bool("diagnostic_summary", true), slog.Bool("sampled", true), slog.Bool("truncated", true)})
	}
	if unknownOverflow != 0 {
		m.logDiagnostic(context.Background(), []slog.Attr{slog.String("component", "achrix.media"), slog.String("operation", "diagnostics"), slog.String("phase", "capacity"), slog.String("reason", "distinct_kind_limit"), slog.String("outcome", "unknown"), slog.Uint64("suppressed_records", unknownOverflow), slog.Bool("diagnostic_summary", true), slog.Bool("sampled", true), slog.Bool("truncated", true)})
	}
}

func diagnosticFields(ctx context.Context, operation, phase string, cause error, outcome string) ([]slog.Attr, string) {
	reason := "dependency_failure"
	var f *diagnosticFault
	if errors.As(cause, &f) {
		phase, reason = f.phase, f.reason
		if outcome == "" {
			outcome = f.outcome
		}
	}
	fields := []slog.Attr{slog.String("component", "achrix.media"), slog.String("operation", operation), slog.String("phase", phase)}
	if f != nil {
		fields = append(fields, f.fields...)
	}
	typed := ""
	var pg *pgconn.PgError
	var errno syscall.Errno
	var network *net.OpError
	var cert *tls.CertificateVerificationError
	switch {
	case errors.As(cause, &pg) && validSQLState(pg.Code):
		typed = pg.Code
		fields = append(fields, slog.String("sqlstate", pg.Code))
		if f == nil {
			reason = "postgres_rejected"
		}
		if pg.SchemaName == "media" {
			fields = append(fields, slog.String("schema", pg.SchemaName))
		}
		if pg.TableName == "assets" || pg.TableName == "schema_migrations" {
			fields = append(fields, slog.String("table", pg.TableName))
		}
		if strings.Contains("|id|state|revision|filename|mime|size|width|height|sha256|created_at|version|checksum|", "|"+pg.ColumnName+"|") && pg.ColumnName != "" {
			fields = append(fields, slog.String("column", pg.ColumnName))
		}
	case errors.As(cause, &errno):
		typed = fmt.Sprint(uintptr(errno))
		fields = append(fields, slog.Uint64("errno", uint64(errno)))
		if f == nil {
			switch errno {
			case syscall.EACCES, syscall.EPERM:
				reason = "permission_denied"
			case syscall.ENOSPC, syscall.EDQUOT:
				reason = "storage_full"
			case syscall.ENOENT:
				reason = "file_missing"
			case syscall.EIO:
				reason = "storage_io"
			case syscall.ECONNREFUSED:
				reason = "connection_refused"
			default:
				reason = "system_failure"
			}
		}
	case errors.Is(cause, io.ErrShortWrite):
		reason = "short_write"
	case errors.Is(cause, io.ErrUnexpectedEOF):
		reason = "unexpected_eof"
	case errors.Is(cause, context.DeadlineExceeded):
		reason = "dependency_deadline"
		if ctx.Err() != nil {
			reason = "caller_deadline"
		}
	case errors.Is(cause, context.Canceled):
		reason = "dependency_canceled"
		if ctx.Err() != nil {
			reason = "caller_canceled"
		}
	case errors.As(cause, &cert):
		reason = "tls_verification"
	case errors.As(cause, &network):
		reason = "network_failure"
	}
	if errors.As(cause, &network) {
		switch network.Op {
		case "dial", "read", "write", "connect", "accept", "close":
			fields = append(fields, slog.String("network_operation", network.Op))
		}
	}
	fields = append(fields, slog.String("reason", reason), slog.String("cause_type", fmt.Sprintf("%T", cause)))
	if outcome != "" {
		fields = append(fields, slog.String("outcome", outcome))
	}
	// A private bounded fingerprint distinguishes arbitrary provider faults with
	// the same typed category. Neither raw text nor digest enters ordinary logs or
	// retained window fields. Truncation is explicit rather than a lossless claim.
	message := cause.Error()
	length := len(message)
	truncated := length > 4096
	if truncated {
		message = message[:4096]
	}
	fingerprint := sha256.Sum256([]byte(message))
	fields = append(fields, slog.Int("cause_length", length), slog.Bool("cause_sample_truncated", truncated))
	return fields, operation + "\x00" + phase + "\x00" + reason + "\x00" + typed + "\x00" + outcome + "\x00" + string(fingerprint[:]) + "\x00" + fmt.Sprint(length)
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

// A cleanup error adds evidence without rewriting an acknowledged domain
// outcome. Already closed transactions/files are expected deferred cleanup.
func (m *Module) cleanupFailure(ctx context.Context, operation, phase string, cause error) {
	cause = unexpectedCleanup(cause)
	if cause == nil {
		return
	}
	m.failures.Add(1)
	m.diagnose(ctx, operation, phase, cause, "primary_unchanged")
}

func unexpectedCleanup(cause error) error {
	if cause == nil || cause == pgx.ErrTxClosed || cause == os.ErrClosed {
		return nil
	}
	if joined, ok := cause.(interface{ Unwrap() []error }); ok {
		var meaningful []error
		for _, part := range joined.Unwrap() {
			if part = unexpectedCleanup(part); part != nil {
				meaningful = append(meaningful, part)
			}
		}
		return errors.Join(meaningful...)
	}
	if wrapped, ok := cause.(interface{ Unwrap() error }); ok && (errors.Is(cause, pgx.ErrTxClosed) || errors.Is(cause, os.ErrClosed)) {
		return unexpectedCleanup(wrapped.Unwrap())
	}
	return cause
}
