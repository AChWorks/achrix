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
	"sync"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/config"
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
	config *pgxpool.Config
	logger *slog.Logger
	mu     sync.RWMutex
	pool   *pgxpool.Pool
}

// New validates bounded pool configuration. The supported fixture permits only a
// local Unix/loopback database without TLS; a future remote profile must require TLS.
func New(dsn string, logger *slog.Logger) (*Store, error) {
	if err := config.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	c.MaxConns = 4
	c.MinConns = 0
	c.ConnConfig.ConnectTimeout = time.Second
	c.MaxConnLifetime = time.Hour
	c.MaxConnIdleTime = time.Minute
	return &Store{config: c, logger: logger}, nil
}

func (s *Store) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "example.notes", Version: ModuleVersion, Provides: []achrix.Capability{{ID: application.Create, Version: 1}, {ID: application.Read, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 1}}}
}
func (s *Store) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := pgxpool.NewWithConfig(ctx, s.config)
	if err != nil {
		return errors.New("database initialization failed")
	}
	s.pool = p
	if err := p.Ping(ctx); err != nil {
		s.logFailure(ctx, "connect", err)
		return errors.New("database connection failed")
	}
	var version int
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return errors.New("database version unavailable")
	}
	if version < 180000 || version >= 190000 {
		return errors.New("unsupported PostgreSQL major")
	}
	// Installation is explicit, never raced by traffic/startup replicas.
	if err := CheckSchema(ctx, p); err != nil {
		return err
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pool == nil {
		return domain.ErrUnavailable
	}
	if err := s.pool.Ping(ctx); err != nil {
		s.logFailure(ctx, "readiness", err)
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
	defer s.mu.RUnlock()
	if s.pool == nil {
		return domain.ErrUnavailable
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO notes.entries(id,body,created_at) VALUES($1,$2,$3)", n.ID, n.Text, n.CreatedAt)
	if err != nil {
		s.logFailure(ctx, "insert", err)
		return domain.ErrUnavailable
	}
	return nil
}
func (s *Store) Find(ctx context.Context, id string) (domain.Note, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pool == nil {
		return domain.Note{}, domain.ErrUnavailable
	}
	var n domain.Note
	err := s.pool.QueryRow(ctx, "SELECT id,body,created_at FROM notes.entries WHERE id=$1", id).Scan(&n.ID, &n.Text, &n.CreatedAt)
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

func (s *Store) logFailure(ctx context.Context, operation string, err error) {
	code := "connection_failure"
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		code = pgerr.Code
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "deadline"
	} else if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	s.logger.ErrorContext(ctx, "database operation failed", "component", "notes.postgresql", "operation", operation, "reason", code)
}

type Migration struct{ ID, SQL string }

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
	if len(migrations) == 0 {
		return errors.New("migration set required")
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS notes; CREATE TABLE IF NOT EXISTS notes.migrations (id text PRIMARY KEY, sha256 text NOT NULL CHECK(length(sha256)=64))`); err != nil {
		return err
	}
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
	known := map[string]string{}
	last := ""
	for _, m := range migrations {
		if m.ID == "" || m.ID <= last || m.SQL == "" {
			return errors.New("ordered unique migrations required")
		}
		last = m.ID
		known[m.ID] = Digest(m)
		if d, ok := applied[m.ID]; ok && d != known[m.ID] {
			return fmt.Errorf("migration %s content changed", m.ID)
		}
	}
	for id := range applied {
		if _, ok := known[id]; !ok {
			return errors.New("unknown applied migration")
		}
	}
	for _, m := range migrations {
		if _, ok := applied[m.ID]; ok {
			continue
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO notes.migrations(id,sha256) VALUES($1,$2)", m.ID, known[m.ID]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func CheckSchema(ctx context.Context, p *pgxpool.Pool) error {
	rows, err := p.Query(ctx, "SELECT id,sha256 FROM notes.migrations ORDER BY id")
	if err != nil {
		return errors.New("schema not installed")
	}
	defer rows.Close()
	expected := Migrations()
	i := 0
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return errors.New("schema unreadable")
		}
		if i >= len(expected) || id != expected[i].ID || d != Digest(expected[i]) {
			return errors.New("incompatible migration identity")
		}
		i++
	}
	if rows.Err() != nil || i != len(expected) {
		return errors.New("incomplete schema")
	}
	return nil
}
