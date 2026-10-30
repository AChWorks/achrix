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
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 61873542136

// Module owns its pool and schema. Products explicitly compose it, install the
// immutable migration, and wire a Service after constructing their Application.
// Stop closes admission, cancels/drains owned DB work, then closes the pool.
type Module struct {
	config      Config
	dbConfig    *pgxpool.Config
	passwords   *passwords
	mu          sync.Mutex
	pool        *pgxpool.Pool
	state       string
	work        context.Context
	cancel      context.CancelFunc
	active      int
	drained     chan struct{}
	logger      *slog.Logger
	failures    atomic.Uint64
	diagnostics diagnosticBudget
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
		constructorDiagnostic(config, logger, "database_config", err)
		return nil, configuration("dsn", safeConfigurationDetail(err))
	}
	db.MaxConns = c.MaxConns
	h, err := newPasswords(c.Password, c.HashConcurrency)
	if err != nil {
		constructorDiagnostic(c, logger, "password_verifier", err)
		return nil, canonicalFailureError(err)
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
		return nil, configuration("dsn", "must contain 1..4096 bytes")
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, diagnosticFault("configuration_parse_failed", "dsn must be a valid PostgreSQL connection configuration", errors.Join(ErrConfiguration, err))
	}
	local := func(host string) bool {
		ip := net.ParseIP(host)
		return strings.HasPrefix(host, "/") || host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	if !local(c.ConnConfig.Host) && (c.ConnConfig.TLSConfig == nil || c.ConnConfig.TLSConfig.InsecureSkipVerify || c.ConnConfig.TLSConfig.ServerName == "") {
		return nil, configuration("dsn.tls", "requires certificate and hostname verification for remote hosts")
	}
	// No plaintext fallback for a remote connection.
	for _, f := range c.ConnConfig.Fallbacks {
		if !local(f.Host) && (f.TLSConfig == nil || f.TLSConfig.InsecureSkipVerify || f.TLSConfig.ServerName == "") {
			return nil, configuration("dsn.fallback_tls", "requires certificate and hostname verification for every remote fallback")
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
	return achrix.Descriptor{ID: "achrix.identity", Version: Version(), Provides: []achrix.Capability{{ID: AccountCreate, Version: 1}, {ID: AccountRead, Version: 1}, {ID: AccountLookup, Version: 1}, {ID: CredentialSet, Version: 1}, {ID: PasswordChange, Version: 1}, {ID: AccountSetEnabled, Version: 1}, {ID: SessionRevokeAll, Version: 1}, {ID: Authentication, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}, {ID: audit.Append, Version: 1}}}
}
func (m *Module) now() time.Time { return m.config.Now().UTC().Truncate(time.Microsecond) }
func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return configuration("context", "requires a deadline")
	}
	m.mu.Lock()
	if m.state != "new" {
		m.mu.Unlock()
		return unavailable("start requires a new Module instance; reconstruct after stop or failed startup")
	}
	m.state = "starting"
	m.mu.Unlock()
	p, err := pgxpool.NewWithConfig(ctx, m.dbConfig.Copy())
	if err != nil {
		return m.failureAt(ctx, "start", "initialize", "not_executed", err)
	}
	m.mu.Lock()
	m.pool = p
	m.mu.Unlock()
	if err = checkEnvironment(ctx, p); err != nil {
		return m.failureAt(ctx, "start", "check_environment", "not_executed", err)
	}
	if err = checkSchema(ctx, p); err != nil {
		return m.failureAt(ctx, "start", "check_schema", "not_executed", err)
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
		return nil, nil, nil, unavailable("Module is not ready; complete startup and check lifecycle diagnostics")
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
	defer m.flushDiagnostics()
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
		m.report(context.WithoutCancel(ctx), "stop", "drain", "", ctx.Err())
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

// Dependencies can wrap supported public identities with private connection,
// configuration or provider details. Rebuild only the identities this Module
// already exposes; never return the original text or unwrap chain.
func canonicalFailureError(err error) error {
	if err == nil {
		return nil
	}
	var safe []error
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrAuthentication, ErrConflict, ErrLimited, ErrUnavailable} {
		if errors.Is(err, category) {
			safe = append(safe, category)
		}
	}
	if len(safe) == 1 {
		return safe[0]
	}
	if len(safe) > 1 {
		return errors.Join(safe...)
	}
	return ErrUnavailable
}

func (m *Module) failure(ctx context.Context, operation string, err error) error {
	return m.failureAt(ctx, operation, "execute", "", err)
}
func (m *Module) failureAt(ctx context.Context, operation, phase, outcome string, err error) error {
	if err == nil {
		return nil
	}
	public := canonicalFailureError(err)
	if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrConflict) || errors.Is(err, ErrLimited) {
		return public
	}
	if phase == "execute" && outcome == "" && ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return public
	}

	// Audit already captured its private append cause. Add only the distinct
	// transaction outcome here, never replay the protected cause callback.
	if err == audit.ErrUnavailable {
		m.failures.Add(1)
		m.diagnostics.emit(ctx, m.logger, nil, operation, phase, outcome, diagnosticFault("audit_append_failed", "Audit append failed; inspect the correlated Audit diagnostic", nil))
		return ErrUnavailable
	}

	m.report(ctx, operation, phase, outcome, err)
	return public
}
func (m *Module) transaction(parent context.Context, operation string, fn func(context.Context, pgx.Tx) error) error {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	tx, err := p.Begin(ctx)
	if err != nil {
		return m.failureAt(ctx, operation, "begin", "not_executed", err)
	}
	outcome := "not_committed"
	defer func() { m.rollback(ctx, operation, outcome, tx) }()
	if err = fn(ctx, tx); err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return m.failureAt(ctx, operation, "execute", outcome, err)
	}
	if err = tx.Commit(ctx); err != nil {
		outcome = commitOutcome(err)
		return m.failureAt(ctx, operation, "commit", outcome, err)
	}
	outcome = "committed"
	return nil
}
func checkEnvironment(ctx context.Context, p *pgxpool.Pool) error {
	var version int
	var encoding string
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_setting('server_encoding')").Scan(&version, &encoding); err != nil {
		return diagnosticFault("environment_query_failed", "cannot read PostgreSQL version and encoding", err)
	}
	if version < 180000 || version >= 190000 {
		return diagnosticFault("unsupported_server_version", "PostgreSQL major version 18 is required", ErrConfiguration, slog.Int("server_version_num", version))
	}
	if encoding != "UTF8" {
		return diagnosticFault("unsupported_server_encoding", "PostgreSQL UTF8 server encoding is required", ErrConfiguration)
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
	var count, version int
	if err := p.QueryRow(ctx, "SELECT count(*),COALESCE(min(checksum),''),COALESCE(min(version),0) FROM identity.schema_migrations").Scan(&count, &got, &version); err != nil {
		reason := "ledger_unreadable"
		var state interface{ SQLState() string }
		if errors.As(err, &state) && (state.SQLState() == "42P01" || state.SQLState() == "3F000") {
			reason = "schema_missing"
		}
		return diagnosticFault(reason, "cannot verify the identity migration ledger", err)
	}
	if count == 0 {
		return diagnosticFault("ledger_incomplete", "the required identity migration ledger entry is missing", ErrConfiguration)
	}
	if count != 1 {
		return diagnosticFault("ledger_incompatible", "the identity migration ledger must contain exactly one entry", ErrConfiguration, slog.Int("ledger_entries", count))
	}
	if version != 1 {
		return diagnosticFault("migration_version_incompatible", "the installed identity migration version is unsupported", ErrConfiguration, slog.Int("installed_version", version), slog.Int("required_version", 1))
	}
	if got != checksum {
		return diagnosticFault("migration_checksum_changed", "the installed identity migration checksum differs from the immutable source", ErrConfiguration)
	}
	rows, err := p.Query(ctx, "SELECT a.id,a.login,a.enabled,a.revision,a.created_at,c.password_hash,c.changed_at,s.token_hash,s.csrf_hash,s.expires_at,s.created_at,s.account_revision FROM identity.accounts a JOIN identity.credentials c ON c.account_id=a.id JOIN identity.sessions s ON s.account_id=a.id WHERE false")
	if err != nil {
		return diagnosticFault("schema_incomplete", "required identity tables or columns cannot be read", err)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return diagnosticFault("schema_read_failed", "required identity columns could not be verified", err)
	}
	return nil
}

// Migrate is an explicit installation action; startup never changes schema.
// Existing callers use the default logger. Products needing protected original
// causes should call MigrateWithDiagnostics with their configured sink.
func Migrate(ctx context.Context, dsn string) error {
	return MigrateWithDiagnostics(ctx, dsn, slog.Default(), nil)
}

// MigrateWithDiagnostics adds product-owned diagnostics without changing the
// public error chain or immutable migration. The hook receives original causes;
// it must apply provider-aware redaction before protected retention/export.
func MigrateWithDiagnostics(ctx context.Context, dsn string, logger *slog.Logger, diagnostic func(context.Context, string, string, error)) error {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Module{config: Config{Diagnostic: diagnostic}, logger: logger}
	defer m.diagnostics.flush(logger)
	fail := func(phase, outcome string, err error) error {
		m.report(ctx, "migrate", phase, outcome, err)
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return configuration("context", "requires a migration deadline")
	}
	c, err := databaseConfig(dsn)
	if err != nil {
		m.report(ctx, "migrate", "database_config", "not_executed", err)
		return configuration("dsn", safeConfigurationDetail(err))
	}
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return fail("initialize", "not_executed", err)
	}
	defer p.Close()
	if err = checkEnvironment(ctx, p); err != nil {
		return fail("check_environment", "not_executed", err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return fail("begin", "not_executed", err)
	}
	outcome := "not_committed"
	cleanupDone := false
	defer func() {
		if !cleanupDone {
			m.rollback(ctx, "migrate", outcome, tx)
		}
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return fail("lock", outcome, err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('identity') IS NOT NULL").Scan(&exists); err != nil {
		return fail("inspect_namespace", outcome, err)
	}
	if exists {
		m.rollback(ctx, "migrate", outcome, tx)
		cleanupDone = true
		if err = checkSchema(ctx, p); err != nil {
			return fail("check_schema", "not_executed", err)
		}
		return nil
	}
	sql, checksum := migrationIdentity()
	if _, err = tx.Exec(ctx, sql); err != nil {
		return fail("ddl", outcome, err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO identity.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		return fail("record_ledger", outcome, err)
	}
	if err = tx.Commit(ctx); err != nil {
		outcome = commitOutcome(err)
		return fail("commit", outcome, err)
	}
	outcome = "committed"
	return nil
}
