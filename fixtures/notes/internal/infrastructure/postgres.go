// SPDX-License-Identifier: MPL-2.0
// Package infrastructure owns PostgreSQL resources/schema of this consumer only.
package infrastructure

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/config"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const ModuleVersion = "0.1.0-fixture"
const migrationLock int64 = 61873542119

type Store struct {
	config     *pgxpool.Config
	logger     *slog.Logger
	diagnostic func(context.Context, string, string, error)
	mu         sync.RWMutex
	pool       *pgxpool.Pool
}

// New validates bounded pool configuration. The supported fixture permits only a
// local Unix/loopback database without TLS; a future remote profile must require TLS.
func New(dsn string, logger *slog.Logger) (*Store, error) {
	return NewWithDiagnostics(dsn, logger, nil)
}

// NewWithDiagnostics preserves original causes at a synchronous protected
// product callback; the callback owns provider-aware redaction before export.
func NewWithDiagnostics(dsn string, logger *slog.Logger, diagnostic func(context.Context, string, string, error)) (*Store, error) {
	if err := config.ValidateDSN(dsn); err != nil {
		if diagnostic != nil {
			cause := err
			if _, parseErr := pgxpool.ParseConfig(dsn); parseErr != nil {
				cause = parseErr
			}
			diagnostic(context.Background(), "configure", "database_config", cause)
		}
		return nil, err
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		if diagnostic != nil {
			diagnostic(context.Background(), "configure", "parse_dsn", err)
		}
		return nil, errors.New("invalid database configuration")
	}
	c.MaxConns = 4
	c.MinConns = 0
	c.ConnConfig.ConnectTimeout = time.Second
	c.MaxConnLifetime = time.Hour
	c.MaxConnIdleTime = time.Minute
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{config: c, logger: logger, diagnostic: diagnostic}, nil
}

func (s *Store) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "example.notes", Version: ModuleVersion, Provides: []achrix.Capability{{ID: application.Create, Version: 1}, {ID: application.Read, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
func (s *Store) Start(ctx context.Context) (result error) {
	s.mu.Lock()
	phase := "initialize"
	var cause error
	defer func() {
		s.mu.Unlock()
		// A product diagnostic callback may inspect component state. Never invoke it
		// while holding the resource lock.
		if result != nil {
			s.logFailure(ctx, phase, cause)
		}
	}()
	p, err := pgxpool.NewWithConfig(ctx, s.config)
	if err != nil {
		cause = err
		return &diagnosticError{message: "database initialization failed", cause: err}
	}
	s.pool = p
	phase = "connect"
	if err := p.Ping(ctx); err != nil {
		cause = err
		return &diagnosticError{message: "database connection failed", cause: err}
	}
	phase = "check_environment"
	if err := CheckEnvironment(ctx, p); err != nil {
		cause = err
		return err
	}
	phase = "check_schema"
	if err := CheckSchema(ctx, p); err != nil {
		cause = err
		return err
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	s.mu.RLock()
	if s.pool == nil {
		s.mu.RUnlock()
		return domain.ErrUnavailable
	}
	err := s.pool.Ping(ctx)
	s.mu.RUnlock()
	if err != nil {
		if s.diagnostic != nil {
			s.diagnostic(ctx, "readiness", "ping", err)
		}
		s.logger.WarnContext(ctx, "database readiness degraded", "component", "notes.postgresql", "operation", "readiness", "phase", "ping", "reason", FailureReason(err))
		return domain.ErrUnavailable
	}
	return nil
}
func (s *Store) Stop(ctx context.Context) error {
	s.mu.Lock()
	p := s.pool
	s.pool = nil
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	// The fixture closes HTTP ingress before Core shutdown. Every operation has a
	// one-second deadline, so any acquired connection returns within that bound.
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) Save(ctx context.Context, n domain.Note) error {
	s.mu.RLock()
	if s.pool == nil {
		s.mu.RUnlock()
		return domain.ErrNotExecuted
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO notes.entries(id,body,created_at) VALUES($1,$2,$3)", n.ID, n.Text, n.CreatedAt)
	s.mu.RUnlock()
	if err != nil {
		s.logFailure(ctx, "insert", err)
		_, outward := writeOutcome(err)
		return outward
	}
	return nil
}
func (s *Store) Find(ctx context.Context, id string) (domain.Note, error) {
	s.mu.RLock()
	if s.pool == nil {
		s.mu.RUnlock()
		return domain.Note{}, domain.ErrUnavailable
	}
	var n domain.Note
	err := s.pool.QueryRow(ctx, "SELECT id,body,created_at FROM notes.entries WHERE id=$1", id).Scan(&n.ID, &n.Text, &n.CreatedAt)
	s.mu.RUnlock()
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Note{}, domain.ErrNotFound
	}
	if err != nil {
		s.logFailure(ctx, "select", err)
		return domain.Note{}, domain.ErrUnavailable
	}
	n.CreatedAt = n.CreatedAt.UTC()
	return n, nil
}

// PostgreSQL rejection confirms this single statement did not commit. A driver
// pre-send guarantee proves non-execution; a generic deadline/transport failure
// proves neither. This classification is not an automatic retry policy.
func writeOutcome(err error) (string, error) {
	if pgconn.SafeToRetry(err) {
		return "not_executed", domain.ErrNotExecuted
	}
	var rejected *pgconn.PgError
	if errors.As(err, &rejected) && rejected.Code != "40003" && !strings.HasPrefix(rejected.Code, "08") {
		severity := rejected.SeverityUnlocalized
		if severity == "" {
			severity = rejected.Severity
		}
		if severity == "ERROR" {
			return "not_committed", domain.ErrNotCommitted
		}
	}
	return "unknown", domain.ErrUnknownOutcome
}

// diagnosticError keeps existing safe error text while retaining an inspectable
// cause or a finite consumer-owned semantic reason. It never formats the cause.
type diagnosticError struct {
	message string
	reason  string
	cause   error
	details []slog.Attr
}

func (e *diagnosticError) Error() string { return e.message }
func (e *diagnosticError) Unwrap() error { return e.cause }

// FailureReason returns only a consumer-owned category or a SQLSTATE token for
// restricted diagnostics. Driver messages and other arbitrary error text stay out.
func FailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var diagnostic *diagnosticError
	if errors.As(err, &diagnostic) && diagnostic.reason != "" {
		return diagnostic.reason
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && len(pgerr.Code) == 5 {
		for _, c := range pgerr.Code {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') {
				return "connection_failure"
			}
		}
		return pgerr.Code
	}
	if reason := diagnosis.IOReason(err); reason != "io_failure" {
		return reason
	}
	return "connection_failure"
}

func failureDetails(err error) []any {
	var detail *diagnosticError
	if !errors.As(err, &detail) {
		return nil
	}
	attrs := make([]any, len(detail.details))
	for i, field := range detail.details {
		attrs[i] = field
	}
	return attrs
}

func (s *Store) logFailure(ctx context.Context, operation string, err error) {
	phase, outcome := operation, "not_applicable"
	if operation == "insert" {
		phase = "execute"
		outcome, _ = writeOutcome(err)
	}
	if s.diagnostic != nil {
		s.diagnostic(ctx, operation, phase, err)
	}
	args := []any{"component", "notes.postgresql", "operation", operation, "phase", phase, "reason", FailureReason(err), "cause_type", fmt.Sprintf("%T", err), "outcome", outcome}
	s.logger.ErrorContext(ctx, "database operation failed", append(args, failureDetails(err)...)...)
}

type Migration struct{ ID, SQL string }

// CheckEnvironment enforces the local fixture's PostgreSQL/Unicode support
// boundary before installation or readiness. Client encoding alone is insufficient.
func CheckEnvironment(ctx context.Context, p *pgxpool.Pool) error {
	var version int
	var encoding string
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::int, current_setting('server_encoding')").Scan(&version, &encoding); err != nil {
		return &diagnosticError{message: "database environment unavailable", cause: err}
	}
	if version < 180000 || version >= 190000 || encoding != "UTF8" {
		return &diagnosticError{message: "unsupported database environment", reason: "unsupported_environment", details: []slog.Attr{slog.Int("server_version_num", version), slog.String("server_encoding", encoding), slog.String("expected_server", "PostgreSQL 18.x UTF8")}}
	}
	return nil
}

func Migrations() []Migration {
	b, err := migrationFiles.ReadFile("migrations/001_notes.sql")
	if err != nil {
		panic(err)
	}
	return []Migration{{ID: "001_notes", SQL: string(b)}}
}
func Digest(m Migration) string { v := sha256.Sum256([]byte(m.SQL)); return hex.EncodeToString(v[:]) }

// Migrate serializes this consumer's complete migration scope with a transaction
// advisory lock. DDL and ledger entries commit atomically; cancellation/connection
// loss rolls back both. No down/destructive migrations exist in this fixture.
func Migrate(ctx context.Context, p *pgxpool.Pool, migrations []Migration) error {
	return MigrateWithDiagnostics(ctx, p, migrations, slog.Default(), nil)
}

// MigrateWithDiagnostics retains operator phase/cause without changing the
// immutable migration contents or the primary commit/rollback result.
func MigrateWithDiagnostics(ctx context.Context, p *pgxpool.Pool, migrations []Migration, logger *slog.Logger, diagnostic func(context.Context, string, string, error)) (result error) {
	if logger == nil {
		logger = slog.Default()
	}
	phase := "check_environment"
	defer func() {
		if result == nil {
			return
		}
		if diagnostic != nil {
			diagnostic(ctx, "migrate", phase, result)
		}
		outcome := "not_committed"
		if phase == "check_environment" || phase == "begin" {
			outcome = "not_executed"
		}
		if phase == "commit" {
			outcome, _ = writeOutcome(result)
			if outcome == "not_executed" || errors.Is(result, pgx.ErrTxCommitRollback) {
				outcome = "not_committed"
			}
		}
		args := []any{"component", "notes.migrations", "operation", "migrate", "phase", phase, "reason", FailureReason(result), "outcome", outcome}
		logger.ErrorContext(ctx, "migration failed", append(args, failureDetails(result)...)...)
	}()
	if err := CheckEnvironment(ctx, p); err != nil {
		return err
	}
	if len(migrations) == 0 {
		return &diagnosticError{message: "migration set required", reason: "migration_set_required"}
	}
	phase = "begin"
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			if diagnostic != nil {
				diagnostic(cleanup, "migrate", "rollback", err)
			}
			logger.ErrorContext(cleanup, "migration cleanup failed", "component", "notes.migrations", "operation", "migrate", "phase", "rollback", "reason", FailureReason(err))
		}
	}()
	phase = "lock"
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return err
	}
	phase = "create_ledger"
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS notes; CREATE TABLE IF NOT EXISTS notes.migrations (id text PRIMARY KEY, sha256 text NOT NULL CHECK(length(sha256)=64))`); err != nil {
		return err
	}
	phase = "read_ledger"
	rows, err := tx.Query(ctx, "SELECT id,sha256 FROM notes.migrations ORDER BY id")
	if err != nil {
		return err
	}
	applied := map[string]string{}
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return err
		}
		applied[id] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	phase = "validate_ledger"
	known := map[string]string{}
	last := ""
	for _, m := range migrations {
		if m.ID == "" || m.ID <= last || m.SQL == "" {
			return &diagnosticError{message: "ordered unique migrations required", reason: "migration_order_invalid"}
		}
		last = m.ID
		known[m.ID] = Digest(m)
		if d, ok := applied[m.ID]; ok && d != known[m.ID] {
			return &diagnosticError{message: fmt.Sprintf("migration %s content changed", m.ID), reason: "migration_changed"}
		}
	}
	for id := range applied {
		if _, ok := known[id]; !ok {
			return &diagnosticError{message: "unknown applied migration", reason: "migration_unknown"}
		}
	}
	for _, m := range migrations {
		if _, ok := applied[m.ID]; ok {
			continue
		}
		phase = "ddl"
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return err
		}
		phase = "record_ledger"
		if _, err := tx.Exec(ctx, "INSERT INTO notes.migrations(id,sha256) VALUES($1,$2)", m.ID, known[m.ID]); err != nil {
			return err
		}
	}
	phase = "commit"
	return tx.Commit(ctx)
}

func CheckSchema(ctx context.Context, p *pgxpool.Pool) error {
	rows, err := p.Query(ctx, "SELECT id,sha256 FROM notes.migrations ORDER BY id")
	if err != nil {
		failure := &diagnosticError{message: "schema not installed", cause: err}
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "42P01" {
			failure.reason = "schema_not_installed"
		}
		return failure
	}
	defer rows.Close()
	expected := Migrations()
	i := 0
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return &diagnosticError{message: "schema unreadable", cause: err}
		}
		if i >= len(expected) || id != expected[i].ID || d != Digest(expected[i]) {
			return &diagnosticError{message: "incompatible migration identity", reason: "schema_incompatible"}
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return &diagnosticError{message: "incomplete schema", cause: err}
	}
	if i != len(expected) {
		return &diagnosticError{message: "incomplete schema", reason: "schema_incomplete"}
	}
	return nil
}
