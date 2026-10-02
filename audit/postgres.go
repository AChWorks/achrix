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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 61873542145

// Module owns only Audit storage, pool, admission and lifecycle. Identity's
// declared dependency orders its shutdown drain before this Module is stopped.
type Module struct {
	config         Config
	dbConfig       *pgxpool.Config
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

func NewPostgres(dsn string, config Config, logger *slog.Logger) (*Module, error) {
	c, err := databaseConfig(dsn)
	if err != nil {
		return nil, err
	}
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
		return nil, ErrConfiguration
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrConfiguration
	}
	if !safeHostTLS(c.ConnConfig.Host, c.ConnConfig.TLSConfig) {
		return nil, ErrConfiguration
	}
	for _, fallback := range c.ConnConfig.Fallbacks {
		if fallback == nil || !safeHostTLS(fallback.Host, fallback.TLSConfig) {
			return nil, ErrConfiguration
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
	return achrix.Descriptor{ID: "achrix.audit", Version: ModuleVersion, Provides: []achrix.Capability{{ID: Append, Version: 1}, {ID: Query, Version: 1}, {ID: Export, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
func (m *Module) now() time.Time { return m.config.Now().UTC().Truncate(time.Microsecond) }
func (m *Module) Start(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
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
		return ErrConfiguration
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
		return nil, nil, nil, ErrUnavailable
	}
	if m.active >= 16 {
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

// FailureCount counts bounded operational failures, not durable accountability.
func (m *Module) FailureCount() uint64 { return m.failures.Load() }
func (m *Module) failure(ctx context.Context, operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrConflict) || errors.Is(err, ErrLimited) || errors.Is(err, ErrInput) || errors.Is(err, ErrUnavailable) {
		return err
	}
	m.failures.Add(1)
	now := time.Now().UnixNano()
	last := m.lastDiagnostic.Load()
	if now-last >= int64(time.Second) && m.lastDiagnostic.CompareAndSwap(last, now) {
		reason := "database_failure"
		var p *pgconn.PgError
		if errors.As(err, &p) && len(p.Code) == 5 {
			valid := true
			for _, r := range p.Code {
				if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') {
					valid = false
				}
			}
			if valid {
				reason = "sqlstate_" + p.Code
			}
		}
		m.logger.ErrorContext(ctx, "audit operation failed", "component", "achrix.audit", "operation", operation, "reason", reason)
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
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(c)
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
	b, _ := migrations.ReadFile("migrations/001_audit.sql")
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}
func checkSchema(ctx context.Context, p *pgxpool.Pool) error {
	_, checksum := migrationIdentity()
	var got string
	var count, version int
	if err := p.QueryRow(ctx, "SELECT count(*),min(checksum),min(version) FROM audit.schema_migrations").Scan(&count, &got, &version); err != nil {
		return err
	}
	if count != 1 || version != 1 || got != checksum {
		return ErrConfiguration
	}
	rows, err := p.Query(ctx, "SELECT seq,id,actor,action,target,authority,outcome,occurred_at FROM audit.records WHERE false")
	if err != nil {
		return err
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var triggerCount int
	err = p.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE tgrelid='audit.records'::regclass AND NOT tgisinternal AND tgenabled='O' AND tgname IN ('records_reject_mutation','records_reject_truncate')").Scan(&triggerCount)
	if err != nil {
		return err
	}
	if triggerCount != 2 {
		return ErrConfiguration
	}
	return nil
}

// Migrate is explicit installation into the product-owned database. Runtime
// startup checks identity and never executes migration SQL.
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
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return ErrUnavailable
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('audit') IS NOT NULL").Scan(&exists); err != nil {
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
	if _, err = tx.Exec(ctx, "INSERT INTO audit.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		return ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
