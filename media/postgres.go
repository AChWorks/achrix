// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
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

const migrationLock int64 = 61873542138

type Module struct {
	config      Config
	dbConfig    *pgxpool.Config
	mu          sync.Mutex
	pool        *pgxpool.Pool
	storage     *localStorage
	state       string
	work        context.Context
	cancel      context.CancelFunc
	active      int
	drained     chan struct{}
	decoders    chan struct{}
	logger      *slog.Logger
	failures    atomic.Uint64
	diagnostics diagnosticBudget
}

func NewPostgres(dsn string, config Config, logger *slog.Logger) (*Module, error) {
	if config.MaxConns < 0 {
		return nil, fmt.Errorf("%w: MaxConns must be zero (default) or positive", ErrConfiguration)
	}
	if config.MaxOperations < 0 {
		return nil, fmt.Errorf("%w: MaxOperations must be zero (default) or positive", ErrConfiguration)
	}
	if config.MaxConns == 0 {
		config.MaxConns = 4
	}
	if config.MaxOperations == 0 {
		config.MaxOperations = 4
	}
	c, err := databaseConfig(dsn)
	if err != nil {
		if config.Diagnostic != nil {
			config.Diagnostic(context.Background(), "configure", "database", err)
		}
		detail := "database connection configuration is invalid"
		var diagnostic *diagnosticFault
		if errors.As(err, &diagnostic) {
			switch diagnostic.reason {
			case "dsn_length":
				detail = "database DSN must contain between 1 and 4096 bytes"
			case "dsn_syntax":
				detail = "database DSN syntax is invalid; use a PostgreSQL URI or keyword=value connection string"
			case "verified_tls_required":
				detail = "database host requires verified TLS; use sslmode=verify-full for remote connections"
			case "fallback_verified_tls_required":
				detail = "every remote database fallback host requires verified TLS; use sslmode=verify-full for all fallback connections"
			}
		}
		return nil, fmt.Errorf("%w: %s", ErrConfiguration, detail)
	}
	c.MaxConns = config.MaxConns
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("%w: the local storage provider requires Linux", ErrConfiguration)
	}
	if config.StorageRoot == "" || len(config.StorageRoot) > 4096 || !strings.HasPrefix(config.StorageRoot, "/") {
		return nil, fmt.Errorf("%w: StorageRoot must be an absolute path of at most 4096 bytes", ErrConfiguration)
	}
	config.AllowedMIMEs, err = selectFormats(config.AllowedMIMEs)
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
	return &Module{config: config, dbConfig: c, state: "new", work: work, cancel: cancel, drained: drained, decoders: make(chan struct{}, 2), logger: logger}, nil
}
func databaseConfig(dsn string) (*pgxpool.Config, error) {
	if len(dsn) == 0 || len(dsn) > 4096 {
		return nil, fault("connection_config", "dsn_length", ErrConfiguration)
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fault("connection_config", "dsn_syntax", errors.Join(ErrConfiguration, err))
	}
	if !safeHostTLS(c.ConnConfig.Host, c.ConnConfig.TLSConfig) {
		return nil, fault("connection_config", "verified_tls_required", ErrConfiguration)
	}
	for i, f := range c.ConnConfig.Fallbacks {
		if f == nil || !safeHostTLS(f.Host, f.TLSConfig) {
			return nil, fault("connection_config", "fallback_verified_tls_required", ErrConfiguration, slog.Int("fallback_index", i))
		}
	}
	// Durable PostgreSQL intent must precede filesystem effects. Explicit session
	// policy overrides DSN/default asynchronous commit; startup checks storage
	// durability settings as well.
	if c.ConnConfig.RuntimeParams == nil {
		c.ConnConfig.RuntimeParams = make(map[string]string)
	}
	c.ConnConfig.RuntimeParams["synchronous_commit"] = "on"
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
func (m *Module) Descriptor() achrix.Descriptor {
	caps := []achrix.Capability{{ID: Create, Version: 1}, {ID: List, Version: 1}, {ID: Read, Version: 1}, {ID: Delete, Version: 1}, {ID: Reconcile, Version: 1}, {ID: PreparePublicImage, Version: 1}}
	return achrix.Descriptor{ID: "achrix.media", Version: Version(), Provides: caps, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%w: Start requires a context deadline", ErrConfiguration)
	}
	m.mu.Lock()
	if m.state != "new" {
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("%w: Start requires a new Module instance; current lifecycle state is %s", ErrUnavailable, state)
	}
	m.state = "starting"
	m.mu.Unlock()
	storage, err := openStorage(m.config.StorageRoot)
	if err != nil {
		return m.failure(ctx, "storage_start", err)
	}
	m.mu.Lock()
	m.storage = storage
	m.mu.Unlock()
	pool, err := pgxpool.NewWithConfig(ctx, m.dbConfig.Copy())
	if err != nil {
		return m.failure(ctx, "initialize", err)
	}
	m.mu.Lock()
	m.pool = pool
	m.mu.Unlock()
	if err = checkEnvironment(ctx, pool); err != nil {
		return m.failure(ctx, "start_environment", err)
	}
	if err = checkSchema(ctx, pool); err != nil {
		return m.failure(ctx, "start_schema", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "starting" || ctx.Err() != nil {
		return ErrUnavailable
	}
	m.state = "ready"
	return nil
}
func (m *Module) acquire(parent context.Context) (context.Context, *pgxpool.Pool, *localStorage, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	if _, ok := parent.Deadline(); !ok {
		return nil, nil, nil, nil, fmt.Errorf("%w: a Media operation requires a context deadline", ErrConfiguration)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "ready" || m.pool == nil || m.storage == nil {
		return nil, nil, nil, nil, ErrUnavailable
	}
	if m.active >= m.config.MaxOperations {
		return nil, nil, nil, nil, ErrLimited
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
	return ctx, m.pool, m.storage, finish, nil
}
func (m *Module) Ready(parent context.Context) error {
	ctx, p, st, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	if err = p.Ping(ctx); err == nil {
		err = st.check()
	}
	if err != nil {
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
		return ctx.Err()
	}
	m.mu.Lock()
	if m.pool != nil {
		m.pool.Close()
		m.pool = nil
	}
	var err error
	if m.storage != nil {
		err = m.storage.close()
		m.storage = nil
	}
	m.state = "stopped"
	m.mu.Unlock()
	if err != nil {
		return m.failure(ctx, "stop_storage", err)
	}
	return nil
}
func (m *Module) FailureCount() uint64 { return m.failures.Load() }

// Dependencies and trusted destinations can wrap safe identities with private
// configuration or payload details. Rebuild only the supported identities and
// never retain the original text or unwrap chain at the public Module boundary.
func canonicalFailureError(err error) error {
	if err == nil {
		return nil
	}
	var safe []error
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrInput, ErrConflict, ErrLimited, ErrNotFound, ErrUnknownOutcome} {
		if errors.Is(err, category) {
			var input *InputError
			if category == ErrInput && errors.As(err, &input) {
				safe = append(safe, &InputError{input.field, input.reason, input.explanation})
				continue
			}
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
	return m.failureResult(ctx, operation, err, canonicalFailureError(err))
}

func (m *Module) failureResult(ctx context.Context, operation string, cause, result error) error {
	unknown := errors.Is(result, ErrUnknownOutcome)
	var diagnostic *diagnosticFault
	if !unknown && !errors.As(cause, &diagnostic) && !hasOperationalMember(ctx, cause) && (errors.Is(result, ErrInput) || errors.Is(result, ErrConflict) || errors.Is(result, ErrLimited) || errors.Is(result, ErrNotFound) || ctx.Err() != nil && (errors.Is(result, context.Canceled) || errors.Is(result, context.DeadlineExceeded))) {
		return result
	}
	m.failures.Add(1)
	outcome := ""
	if unknown {
		outcome = "unknown"
	}
	m.diagnose(ctx, operation, operation, cause, outcome)
	return result
}
func checkEnvironment(ctx context.Context, p *pgxpool.Pool) error {
	var version int
	var encoding, commit string
	var fsync, fullPages bool
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_setting('server_encoding'),current_setting('fsync')::boolean,current_setting('full_page_writes')::boolean,current_setting('synchronous_commit')").Scan(&version, &encoding, &fsync, &fullPages, &commit); err != nil {
		return fault("environment_query", "environment_query_failed", err)
	}
	if version < 180000 || version >= 190000 {
		return fault("environment_check", "unsupported_postgres", ErrConfiguration, slog.Int("server_version_num", version))
	}
	if encoding != "UTF8" {
		return fault("environment_check", "utf8_required", ErrConfiguration)
	}
	if !fsync || !fullPages || commit != "on" {
		return fault("environment_check", "durability_settings", ErrConfiguration, slog.Bool("fsync", fsync), slog.Bool("full_page_writes", fullPages), slog.Bool("synchronous_commit", commit == "on"))
	}
	return nil
}
func migrationIdentity() (string, string) {
	b, _ := migrations.ReadFile("migrations/001_media.sql")
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}

type schemaQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type migration struct {
	sql, checksum string
}

func migrationPlan() []migration {
	plan := make([]migration, 0, 3)
	for _, name := range []string{"001_media.sql", "002_common_formats.sql", "003_private_svg.sql"} {
		b, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			panic("missing embedded Media migration")
		}
		sum := sha256.Sum256(b)
		plan = append(plan, migration{string(b), hex.EncodeToString(sum[:])})
	}
	return plan
}

// An existing schema must have a nonempty exact ordered prefix of this source's
// immutable ledger. Unknown, missing, reordered or changed entries fail closed.
func ledgerVersion(ctx context.Context, q schemaQuery, plan []migration) (int, error) {
	rows, err := q.Query(ctx, "SELECT version,checksum FROM media.schema_migrations ORDER BY version")
	if err != nil {
		return 0, fault("ledger_query", "ledger_unavailable", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return 0, fault("ledger_scan", "ledger_invalid_row", err, slog.Int("entry", count+1))
		}
		if count >= len(plan) || version > len(plan) {
			return 0, fault("ledger_validate", "ledger_unknown_version", ErrConfiguration, slog.Int("version", version))
		}
		if version != count+1 {
			return 0, fault("ledger_validate", "ledger_noncontiguous", ErrConfiguration, slog.Int("expected_version", count+1), slog.Int("version", version))
		}
		if checksum != plan[count].checksum {
			return 0, fault("ledger_validate", "ledger_checksum_changed", ErrConfiguration, slog.Int("version", version))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fault("ledger_read", "ledger_read_failed", err)
	}
	if count == 0 {
		return 0, fault("ledger_validate", "ledger_empty", ErrConfiguration)
	}
	return count, nil
}
func checkAssetSchema(ctx context.Context, q schemaQuery) error {
	rows, err := q.Query(ctx, "SELECT id,state,revision,filename,mime,size,width,height,sha256,created_at FROM media.assets WHERE false")
	if err != nil {
		return fault("asset_schema", "asset_schema_invalid", err)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fault("asset_schema", "asset_schema_invalid", err)
	}
	return nil
}
func checkSchema(ctx context.Context, p *pgxpool.Pool) error {
	plan := migrationPlan()
	version, err := ledgerVersion(ctx, p, plan)
	if err != nil {
		return err
	}
	if version != len(plan) {
		return fault("ledger_validate", "migration_required", ErrConfiguration, slog.Int("installed_version", version), slog.Int("required_version", len(plan)))
	}
	return checkAssetSchema(ctx, p)
}

// Migrate explicitly installs or advances immutable PostgreSQL metadata under
// one transactional advisory lock. Start never does DDL. A source rollback
// cannot reverse the ledger or durable filesystem effects.
func Migrate(ctx context.Context, dsn string) error {
	return MigrateWithDiagnostics(ctx, dsn, nil, nil)
}

// MigrateWithDiagnostics preserves startup/migration causes at a protected
// product-owned boundary. Public errors retain the existing safe identities.
// The hook must redact provider secrets before storing/exporting its evidence.
func MigrateWithDiagnostics(ctx context.Context, dsn string, logger *slog.Logger, diagnostic func(context.Context, string, string, error)) error {
	m := &Module{logger: logger, config: Config{Diagnostic: diagnostic}}
	defer m.flushDiagnostics()
	fail := func(phase string, cause error, result error) error {
		outcome := "not_executed"
		if errors.Is(result, ErrUnknownOutcome) {
			outcome = "unknown"
		}
		m.diagnose(ctx, "migrate", phase, cause, outcome)
		return result
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%w: Migrate requires a context deadline", ErrConfiguration)
	}
	c, err := databaseConfig(dsn)
	if err != nil {
		return fail("connection_config", err, fmt.Errorf("%w: check database connection syntax and verified TLS configuration", ErrConfiguration))
	}
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return fail("pool_initialize", err, ErrUnavailable)
	}
	defer p.Close()
	if err = checkEnvironment(ctx, p); err != nil {
		return fail("environment", err, ErrUnavailable)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return fail("begin", err, ErrUnavailable)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		m.cleanupFailure(c, "migrate", "rollback", tx.Rollback(c))
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
		return fail("lock", err, ErrUnavailable)
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('media') IS NOT NULL").Scan(&exists); err != nil {
		return fail("namespace", err, ErrUnavailable)
	}
	plan := migrationPlan()
	version := 0
	if exists {
		version, err = ledgerVersion(ctx, tx, plan)
		if err != nil {
			return fail("ledger", err, ErrUnavailable)
		}
	}
	for i := version; i < len(plan); i++ {
		if _, err = tx.Exec(ctx, plan[i].sql); err != nil {
			return fail("ddl", fault("ddl", "migration_ddl_failed", err, slog.Int("version", i+1)), ErrUnavailable)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO media.schema_migrations(version,checksum) VALUES($1,$2)", i+1, plan[i].checksum); err != nil {
			return fail("ledger_insert", fault("ledger_insert", "ledger_insert_failed", err, slog.Int("version", i+1)), ErrUnavailable)
		}
	}
	if err = checkAssetSchema(ctx, tx); err != nil {
		return fail("asset_schema", err, ErrUnavailable)
	}
	if err = tx.Commit(ctx); err != nil {
		return fail("commit", err, ErrUnknownOutcome)
	}
	return nil
}

const assetColumns = "id,state,revision,filename,mime,size,width,height,sha256,created_at"

func scanAsset(row pgx.Row) (Asset, error) {
	var a Asset
	err := row.Scan(&a.ID, &a.State, &a.Revision, &a.Filename, &a.MIME, &a.Size, &a.Width, &a.Height, &a.SHA256, &a.CreatedAt)
	a.CreatedAt = a.CreatedAt.UTC()
	return a, err
}
func findAsset(ctx context.Context, c *pgxpool.Conn, id string) (Asset, error) {
	a, err := scanAsset(c.QueryRow(ctx, "SELECT "+assetColumns+" FROM media.assets WHERE id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	return a, err
}

// Session advisory locks span filesystem effects without an open DB transaction.
// Failed unlock discards the physical connection so a pooled session cannot retain
// a lock. Busy locks fail immediately rather than creating an unbounded queue.
func lockAsset(ctx context.Context, p *pgxpool.Pool, id string, shared bool) (*pgxpool.Conn, func() error, error) {
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	lock, unlock := "pg_try_advisory_lock", "pg_advisory_unlock"
	if shared {
		lock, unlock = "pg_try_advisory_lock_shared", "pg_advisory_unlock_shared"
	}
	var ok bool
	if err = c.QueryRow(ctx, "SELECT "+lock+"($1)", lockKey(id)).Scan(&ok); err != nil || !ok {
		// An unacknowledged lock might have succeeded; never return that session.
		if err != nil {
			conn := c.Hijack()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			if closeErr := unexpectedCleanup(conn.Close(cleanup)); closeErr != nil {
				err = errors.Join(err, fault("lock_connection_close", "lock_discard_close_failed", closeErr))
			}
			cancel()
		} else {
			c.Release()
		}
		if err == nil {
			err = ErrConflict
		}
		return nil, nil, err
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		var released bool
		if err := c.QueryRow(cleanup, "SELECT "+unlock+"($1)", lockKey(id)).Scan(&released); err != nil || !released {
			conn := c.Hijack()
			if err == nil {
				err = fault("asset_unlock", "lock_not_owned", ErrUnavailable)
			}
			return errors.Join(fault("asset_unlock", "asset_unlock_failed", err), conn.Close(cleanup))
		} else {
			c.Release()
		}
		return nil
	}
	return c, release, nil
}
func (m *Module) status(parent context.Context, id string) (Asset, error) {
	ctx, p, _, finish, err := m.acquire(parent)
	if err != nil {
		return Asset{}, err
	}
	defer finish()
	c, err := p.Acquire(ctx)
	if err != nil {
		return Asset{}, m.failure(ctx, "status", err)
	}
	defer c.Release()
	a, err := findAsset(ctx, c, id)
	if err != nil {
		return Asset{}, m.failure(ctx, "status", err)
	}
	return a, nil
}
func (m *Module) list(parent context.Context, after string, limit int) (Page, error) {
	ctx, p, _, finish, err := m.acquire(parent)
	if err != nil {
		return Page{}, err
	}
	defer finish()
	rows, err := p.Query(ctx, "SELECT "+assetColumns+" FROM media.assets WHERE state='ready' AND id>$1 ORDER BY id LIMIT $2", after, limit+1)
	if err != nil {
		return Page{}, m.failure(ctx, "list", err)
	}
	defer rows.Close()
	result := Page{Assets: make([]Asset, 0, limit)}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return Page{}, m.failure(ctx, "list", err)
		}
		if len(result.Assets) == limit {
			result.NextCursor = encodeCursor(result.Assets[len(result.Assets)-1].ID)
			break
		}
		result.Assets = append(result.Assets, a)
	}
	if err = rows.Err(); err != nil {
		return Page{}, m.failure(ctx, "list", err)
	}
	return result, nil
}
