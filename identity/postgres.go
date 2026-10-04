// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 61873542136

// Module owns its pool and schema. Products explicitly compose it, install the
// immutable migration, and wire a Service after constructing their Application.
// Stop closes admission, cancels/drains owned DB work, then closes the pool.
type Module struct {
	config         Config
	dbConfig       *pgxpool.Config
	passwords      *passwords
	mu             sync.Mutex
	pool           *pgxpool.Pool
	state          string
	work           context.Context
	cancel         context.CancelFunc
	active         int
	drained        chan struct{}
	logger         *slog.Logger
	failures       atomic.Uint64
	lastDiagnostic atomic.Int64
}

// NewPostgres validates configuration without network/database side effects.
// Runtime DSN sources remain product-owned. Unix/loopback is the measured dev
// profile; a remote DSN requires certificate-verified TLS and earns no support
// claim merely by passing this defensive preflight.
func NewPostgres(dsn string, config Config, logger *slog.Logger) (*Module, error) {
	c, err := config.defaults()
	if err != nil {
		return nil, err
	}
	db, err := databaseConfig(dsn)
	if err != nil {
		return nil, err
	}
	db.MaxConns = c.MaxConns
	h, err := newPasswords(c.Password, c.HashConcurrency)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	work, cancel := context.WithCancel(context.Background())
	drained := make(chan struct{})
	close(drained)
	return &Module{config: c, dbConfig: db, passwords: h, state: "new", work: work, cancel: cancel, drained: drained, logger: logger}, nil
}
func databaseConfig(dsn string) (*pgxpool.Config, error) {
	if len(dsn) == 0 || len(dsn) > 4096 {
		return nil, ErrConfiguration
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrConfiguration
	}
	local := func(host string) bool {
		ip := net.ParseIP(host)
		return strings.HasPrefix(host, "/") || host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	if !local(c.ConnConfig.Host) && (c.ConnConfig.TLSConfig == nil || c.ConnConfig.TLSConfig.InsecureSkipVerify || c.ConnConfig.TLSConfig.ServerName == "") {
		return nil, ErrConfiguration
	}
	// No plaintext fallback for a remote connection.
	for _, f := range c.ConnConfig.Fallbacks {
		if !local(f.Host) && (f.TLSConfig == nil || f.TLSConfig.InsecureSkipVerify || f.TLSConfig.ServerName == "") {
			return nil, ErrConfiguration
		}
	}
	c.MaxConns = 4
	c.MinConns = 0
	c.MinIdleConns = 0
	c.HealthCheckPeriod = time.Minute
	c.MaxConnLifetimeJitter = 0
	c.PingTimeout = time.Second
	c.ConnConfig.ConnectTimeout = time.Second
	c.MaxConnIdleTime = time.Minute
	c.MaxConnLifetime = time.Hour
	return c, nil
}
func (m *Module) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "achrix.identity", Version: achrix.Version(), Provides: []achrix.Capability{{ID: AccountCreate, Version: 1}, {ID: AccountRead, Version: 1}, {ID: AccountLookup, Version: 1}, {ID: CredentialSet, Version: 1}, {ID: PasswordChange, Version: 1}, {ID: AccountSetEnabled, Version: 1}, {ID: SessionRevokeAll, Version: 1}, {ID: Authentication, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}, {ID: audit.Append, Version: 1}}}
}
func (m *Module) now() time.Time { return m.config.Now().UTC().Truncate(time.Microsecond) }
func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrConfiguration
	}
	m.mu.Lock()
	if m.state != "new" {
		m.mu.Unlock()
		return ErrUnavailable
	}
	m.state = "starting"
	m.mu.Unlock()
	p, err := pgxpool.NewWithConfig(ctx, m.dbConfig.Copy())
	if err != nil {
		return m.failure(ctx, "initialize", err)
	}
	m.mu.Lock()
	m.pool = p
	m.mu.Unlock()
	if err = checkEnvironment(ctx, p); err == nil {
		err = checkSchema(ctx, p)
	}
	if err != nil {
		return m.failure(ctx, "start", err)
	}
	m.mu.Lock()
	if m.state != "starting" || ctx.Err() != nil {
		m.mu.Unlock()
		return ErrUnavailable
	}
	m.state = "ready"
	m.mu.Unlock()
	return nil
}
func (m *Module) acquire(parent context.Context) (context.Context, *pgxpool.Pool, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, nil, err
	}
	if _, ok := parent.Deadline(); !ok {
		return nil, nil, nil, ErrConfiguration
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "ready" || m.pool == nil {
		return nil, nil, nil, ErrUnavailable
	}
	if m.active >= m.config.MaxOperations {
		return nil, nil, nil, ErrLimited
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(m.work, cancel)
	if m.active == 0 {
		m.drained = make(chan struct{})
	}
	m.active++
	finish := func() {
		stop()
		cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.active--
		if m.active == 0 {
			close(m.drained)
		}
	}
	return ctx, m.pool, finish, nil
}
func (m *Module) Ready(parent context.Context) error {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	if err = p.Ping(ctx); err != nil {
		return m.failure(ctx, "readiness", err)
	}
	return nil
}
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.state == "stopped" {
		m.mu.Unlock()
		return nil
	}
	m.state = "stopping"
	m.cancel()
	drained := m.drained
	m.mu.Unlock()
	select {
	case <-drained:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pool != nil {
		m.pool.Close()
		m.pool = nil
	}
	m.state = "stopped"
	return nil
}

// FailureCount is fixed-cardinality operational evidence, not durable Audit.
// Expected denial/authentication/invalid/limited traffic does not emit logs.
func (m *Module) FailureCount() uint64 { return m.failures.Load() }
func (m *Module) failure(ctx context.Context, operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrConflict) || errors.Is(err, ErrLimited) {
		return err
	}
	m.failures.Add(1)
	now := time.Now().UnixNano()
	last := m.lastDiagnostic.Load()
	if now-last >= int64(time.Second) && m.lastDiagnostic.CompareAndSwap(last, now) {
		reason := "database_failure"
		var pe *pgconn.PgError
		if errors.As(err, &pe) && len(pe.Code) == 5 {
			valid := true
			for _, r := range pe.Code {
				if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') {
					valid = false
				}
			}
			if valid {
				reason = "sqlstate_" + pe.Code
			}
		}
		m.logger.ErrorContext(ctx, "identity operation failed", "component", "achrix.identity", "operation", operation, "reason", reason)
	}
	return ErrUnavailable
}
func (m *Module) transaction(parent context.Context, operation string, fn func(context.Context, pgx.Tx) error) error {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	tx, err := p.Begin(ctx)
	if err != nil {
		return m.failure(ctx, operation, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err = fn(ctx, tx); err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return m.failure(ctx, operation, err)
	}
	return nil
}
func checkEnvironment(ctx context.Context, p *pgxpool.Pool) error {
	var version int
	var encoding string
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_setting('server_encoding')").Scan(&version, &encoding); err != nil {
		return err
	}
	if version < 180000 || version >= 190000 || encoding != "UTF8" {
		return ErrConfiguration
	}
	return nil
}
func migrationIdentity() (string, string) {
	b, _ := migrations.ReadFile("migrations/001_identity.sql")
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}
func checkSchema(ctx context.Context, p *pgxpool.Pool) error {
	_, checksum := migrationIdentity()
	var got string
	var count int
	if err := p.QueryRow(ctx, "SELECT count(*),min(checksum) FROM identity.schema_migrations").Scan(&count, &got); err != nil {
		return err
	}
	if count != 1 || got != checksum {
		return ErrConfiguration
	}
	var version int
	if err := p.QueryRow(ctx, "SELECT version FROM identity.schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return ErrConfiguration
	}
	// Resolve every runtime column before admitting traffic. PostgreSQL constraints
	// own value invariants; migration identity detects unsupported schema versions.
	rows, err := p.Query(ctx, "SELECT a.id,a.login,a.enabled,a.revision,a.created_at,c.password_hash,c.changed_at,s.token_hash,s.csrf_hash,s.expires_at,s.created_at,s.account_revision FROM identity.accounts a JOIN identity.credentials c ON c.account_id=a.id JOIN identity.sessions s ON s.account_id=a.id WHERE false")
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// Migrate is an explicit product installation action, never part of Start.
// The schema and migration identity commit together. A repeated installation
// verifies the immutable checksum rather than reinterpreting existing data.
func Migrate(ctx context.Context, dsn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrConfiguration
	}
	c, err := databaseConfig(dsn)
	if err != nil {
		return err
	}
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return ErrUnavailable
	}
	defer p.Close()
	if err = checkEnvironment(ctx, p); err != nil {
		return ErrUnavailable
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return ErrUnavailable
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('identity') IS NOT NULL").Scan(&exists); err != nil {
		return ErrUnavailable
	}
	if exists {
		_ = tx.Rollback(ctx)
		if err = checkSchema(ctx, p); err != nil {
			return ErrUnavailable
		}
		return nil
	}
	sql, checksum := migrationIdentity()
	if _, err = tx.Exec(ctx, sql); err != nil {
		return ErrUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO identity.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		return ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
