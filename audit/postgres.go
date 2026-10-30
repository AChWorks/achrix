// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 61873542145

// Module owns only Audit storage, pool, admission and lifecycle. Identity's
// declared dependency orders its shutdown drain before this Module is stopped.
type Module struct {
	config      Config
	dbConfig    *pgxpool.Config
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

func NewPostgres(dsn string, config Config, logger *slog.Logger) (*Module, error) {
	if config.MaxConns < 0 {
		return nil, configuration("MaxConns", "must be zero/default or positive")
	}
	if config.MaxOperations < 0 || config.MaxOperations == 1 {
		return nil, configuration("MaxOperations", "must be zero/default or at least two")
	}
	if config.MaxConns == 0 {
		config.MaxConns = 4
	}
	if config.MaxOperations == 0 {
		config.MaxOperations = 16
	}
	c, err := databaseConfig(dsn)
	if err != nil {
		constructorDiagnostic(config, logger, "database_config", err)
		return nil, configuration("dsn", safeConfigurationDetail(err))
	}
	c.MaxConns = config.MaxConns
	if config.Now == nil {
		config.Now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	work, cancel := context.WithCancel(context.Background())
	drained := make(chan struct{})
	close(drained)
	return &Module{config: config, dbConfig: c, state: "new", work: work, cancel: cancel, drained: drained, logger: logger}, nil
}

func databaseConfig(dsn string) (*pgxpool.Config, error) {
	if len(dsn) == 0 || len(dsn) > 4096 {
		return nil, configuration("dsn", "must contain 1..4096 bytes")
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, diagnosticFault("configuration_parse_failed", "dsn must be a valid PostgreSQL connection configuration", errors.Join(ErrConfiguration, err))
	}
	if !safeHostTLS(c.ConnConfig.Host, c.ConnConfig.TLSConfig) {
		return nil, configuration("dsn.tls", "requires certificate and hostname verification for remote hosts")
	}
	for _, fallback := range c.ConnConfig.Fallbacks {
		if fallback == nil || !safeHostTLS(fallback.Host, fallback.TLSConfig) {
			return nil, configuration("dsn.fallback_tls", "requires valid certificate-verified remote fallbacks")
		}
	}
	c.MaxConns = 4
	c.MinConns = 0
	c.MinIdleConns = 0
	c.HealthCheckPeriod = time.Minute
	c.MaxConnLifetime = time.Hour
	c.MaxConnLifetimeJitter = 0
	c.MaxConnIdleTime = time.Minute
	c.PingTimeout = time.Second
	c.ConnConfig.ConnectTimeout = time.Second
	return c, nil
}
func safeHostTLS(host string, c *tls.Config) bool {
	ip := net.ParseIP(host)
	local := strings.HasPrefix(host, "/") || host == "localhost" || ip != nil && ip.IsLoopback()
	return local || c != nil && !c.InsecureSkipVerify && c.ServerName != ""
}

// Driver-generated callbacks have no public comparable semantic identity. When
// one is configured (e.g. target_session_attrs), require the same source string
// as well as all normalized settings; this conservatively rejects ambiguity.
func sameDatabase(a, b *pgx.ConnConfig) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Host != b.Host || a.Port != b.Port || a.Database != b.Database || a.User != b.User || a.Password != b.Password || a.KerberosSrvName != b.KerberosSrvName || a.KerberosSpn != b.KerberosSpn || a.SSLNegotiation != b.SSLNegotiation || a.ChannelBinding != b.ChannelBinding || a.RequireAuth != b.RequireAuth || a.MinProtocolVersion != b.MinProtocolVersion || a.MaxProtocolVersion != b.MaxProtocolVersion || a.MaxProtocolMessageBodyLen != b.MaxProtocolMessageBodyLen || a.StatementCacheCapacity != b.StatementCacheCapacity || a.DescriptionCacheCapacity != b.DescriptionCacheCapacity || a.DefaultQueryExecMode != b.DefaultQueryExecMode || !reflect.DeepEqual(a.RuntimeParams, b.RuntimeParams) || !sameTLS(a.TLSConfig, b.TLSConfig) || len(a.Fallbacks) != len(b.Fallbacks) {
		return false
	}
	if (a.ValidateConnect != nil || b.ValidateConnect != nil || a.OAuthTokenProvider != nil || b.OAuthTokenProvider != nil) && a.ConnString() != b.ConnString() {
		return false
	}
	for i, f := range a.Fallbacks {
		g := b.Fallbacks[i]
		if f == nil || g == nil || f.Host != g.Host || f.Port != g.Port || !sameTLS(f.TLSConfig, g.TLSConfig) {
			return false
		}
	}
	return true
}
func samePool(a, b *x509.CertPool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(b)
}
func sameTLS(a, b *tls.Config) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ServerName == b.ServerName && a.InsecureSkipVerify == b.InsecureSkipVerify && a.MinVersion == b.MinVersion && a.MaxVersion == b.MaxVersion && samePool(a.RootCAs, b.RootCAs) && samePool(a.ClientCAs, b.ClientCAs) && reflect.DeepEqual(a.Certificates, b.Certificates) && reflect.DeepEqual(a.NextProtos, b.NextProtos) && reflect.DeepEqual(a.CipherSuites, b.CipherSuites) && reflect.DeepEqual(a.CurvePreferences, b.CurvePreferences) && (a.VerifyPeerCertificate == nil) == (b.VerifyPeerCertificate == nil) && (a.VerifyConnection == nil) == (b.VerifyConnection == nil)
}

func (m *Module) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "achrix.audit", Version: Version(), Provides: []achrix.Capability{{ID: Append, Version: 1}, {ID: Query, Version: 1}, {ID: Export, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
func (m *Module) now() time.Time { return m.config.Now().UTC().Truncate(time.Microsecond) }
func (m *Module) Start(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
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
	defer m.mu.Unlock()
	if m.state != "starting" || ctx.Err() != nil {
		return ErrUnavailable
	}
	m.state = "ready"
	return nil
}
func (m *Module) admission(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return configuration("context", "requires a deadline")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "ready" || m.pool == nil {
		return ErrUnavailable
	}
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

// FailureCount counts bounded operational failures, not durable accountability.
func (m *Module) FailureCount() uint64 { return m.failures.Load() }

// Dependencies can wrap supported public identities with private connection,
// configuration or provider details. Rebuild only the identities this Module
// already exposes; never return the original text or unwrap chain.
func canonicalFailureError(err error) error {
	if err == nil {
		return nil
	}
	var safe []error
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrConflict, ErrLimited, ErrInput, ErrUnavailable} {
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
	if errors.Is(err, ErrInput) || errors.Is(err, ErrConflict) || errors.Is(err, ErrLimited) {
		return public
	}
	if phase == "execute" && outcome == "" && ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return public
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
		// AppendInTx owns and has already captured the dependency cause. This
		// owner adds the transaction result without duplicating its hook.
		if err == ErrUnavailable || (ctx.Err() == nil && (err == context.Canceled || err == context.DeadlineExceeded)) {
			m.diagnostics.emit(ctx, m.logger, nil, operation, "transaction_result", outcome, diagnosticFault("append_not_committed", "Audit append failed; this transaction was not committed", nil))
			return canonicalFailureError(err)
		}
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
	b, _ := migrations.ReadFile("migrations/001_audit.sql")
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}
func checkSchema(ctx context.Context, p *pgxpool.Pool) error {
	_, checksum := migrationIdentity()
	var got string
	var count, version int
	if err := p.QueryRow(ctx, "SELECT count(*),COALESCE(min(checksum),''),COALESCE(min(version),0) FROM audit.schema_migrations").Scan(&count, &got, &version); err != nil {
		reason := "ledger_unreadable"
		var state interface{ SQLState() string }
		if errors.As(err, &state) && (state.SQLState() == "42P01" || state.SQLState() == "3F000") {
			reason = "schema_missing"
		}
		return diagnosticFault(reason, "cannot verify the audit migration ledger", err)
	}
	if count == 0 {
		return diagnosticFault("ledger_incomplete", "the required audit migration ledger entry is missing", ErrConfiguration)
	}
	if count != 1 {
		return diagnosticFault("ledger_incompatible", "the audit migration ledger must contain exactly one entry", ErrConfiguration, slog.Int("ledger_entries", count))
	}
	if version != 1 {
		return diagnosticFault("migration_version_incompatible", "the installed audit migration version is unsupported", ErrConfiguration, slog.Int("installed_version", version), slog.Int("required_version", 1))
	}
	if got != checksum {
		return diagnosticFault("migration_checksum_changed", "the installed audit migration checksum differs from the immutable source", ErrConfiguration)
	}
	rows, err := p.Query(ctx, "SELECT seq,id,actor,action,target,authority,outcome,occurred_at FROM audit.records WHERE false")
	if err != nil {
		return diagnosticFault("schema_incomplete", "required audit tables or columns cannot be read", err)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return diagnosticFault("schema_read_failed", "required audit columns could not be verified", err)
	}

	var guardCount int
	err = p.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE tgrelid='audit.records'::regclass AND NOT tgisinternal AND tgenabled='O' AND tgname IN ('records_reject_mutation','records_reject_truncate')").Scan(&guardCount)
	if err != nil {
		return diagnosticFault("guard_check_failed", "cannot verify Audit mutation guards", err)
	}
	if guardCount != 2 {
		return diagnosticFault("guard_missing", "required Audit mutation guards are missing or disabled", ErrConfiguration, slog.Int("enabled_guards", guardCount), slog.Int("required_guards", 2))
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
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('audit') IS NOT NULL").Scan(&exists); err != nil {
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
	if _, err = tx.Exec(ctx, "INSERT INTO audit.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		return fail("record_ledger", outcome, err)
	}
	if err = tx.Commit(ctx); err != nil {
		outcome = commitOutcome(err)
		return fail("commit", outcome, err)
	}
	outcome = "committed"
	return nil
}
