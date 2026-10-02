// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
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
	config         Config
	dbConfig       *pgxpool.Config
	mu             sync.Mutex
	pool           *pgxpool.Pool
	storage        *localStorage
	state          string
	work           context.Context
	cancel         context.CancelFunc
	active         int
	drained        chan struct{}
	decoders       chan struct{}
	logger         *slog.Logger
	failures       atomic.Uint64
	lastDiagnostic atomic.Int64
}

func NewPostgres(dsn string, config Config, logger *slog.Logger) (*Module, error) {
	c, err := databaseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || config.StorageRoot == "" || len(config.StorageRoot) > 4096 || !strings.HasPrefix(config.StorageRoot, "/") {
		return nil, ErrConfiguration
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
		return nil, ErrConfiguration
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrConfiguration
	}
	if !safeHostTLS(c.ConnConfig.Host, c.ConnConfig.TLSConfig) {
		return nil, ErrConfiguration
	}
	for _, f := range c.ConnConfig.Fallbacks {
		if f == nil || !safeHostTLS(f.Host, f.TLSConfig) {
			return nil, ErrConfiguration
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
	caps := []achrix.Capability{{ID: Create, Version: 1}, {ID: List, Version: 1}, {ID: Read, Version: 1}, {ID: Delete, Version: 1}, {ID: Reconcile, Version: 1}}
	return achrix.Descriptor{ID: "achrix.media", Version: achrix.Version(), Provides: caps, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
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
	if err = checkEnvironment(ctx, pool); err == nil {
		err = checkSchema(ctx, pool)
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
func (m *Module) acquire(parent context.Context) (context.Context, *pgxpool.Pool, *localStorage, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	if _, ok := parent.Deadline(); !ok {
		return nil, nil, nil, nil, ErrConfiguration
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "ready" || m.pool == nil || m.storage == nil {
		return nil, nil, nil, nil, ErrUnavailable
	}
	if m.active >= 4 {
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
	var err error
	if m.storage != nil {
		err = m.storage.close()
		m.storage = nil
	}
	m.state = "stopped"
	return err
}
func (m *Module) FailureCount() uint64 { return m.failures.Load() }
func (m *Module) failure(ctx context.Context, operation string, err error) error {
	if !errors.Is(err, ErrUnknownOutcome) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) || errors.Is(err, ErrInput) || errors.Is(err, ErrConflict) || errors.Is(err, ErrLimited) || errors.Is(err, ErrNotFound) {
		return err
	}
	m.failures.Add(1)
	now := time.Now().UnixNano()
	last := m.lastDiagnostic.Load()
	if now-last >= int64(time.Second) && m.lastDiagnostic.CompareAndSwap(last, now) {
		reason := "dependency_failure"
		if errors.Is(err, ErrUnknownOutcome) {
			reason = "unknown_outcome"
		}
		m.logger.ErrorContext(ctx, "media operation failed", "component", "achrix.media", "operation", operation, "reason", reason)
	}
	if errors.Is(err, ErrUnknownOutcome) {
		return err
	}
	return ErrUnavailable
}
func checkEnvironment(ctx context.Context, p *pgxpool.Pool) error {
	var version int
	var encoding, commit string
	var fsync, fullPages bool
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_setting('server_encoding'),current_setting('fsync')::boolean,current_setting('full_page_writes')::boolean,current_setting('synchronous_commit')").Scan(&version, &encoding, &fsync, &fullPages, &commit); err != nil {
		return err
	}
	if version < 180000 || version >= 190000 || encoding != "UTF8" || !fsync || !fullPages || commit != "on" {
		return ErrConfiguration
	}
	return nil
}
func migrationIdentity() (string, string) {
	b, _ := migrations.ReadFile("migrations/001_media.sql")
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}
func checkSchema(ctx context.Context, p *pgxpool.Pool) error {
	_, checksum := migrationIdentity()
	var got string
	var count, version int
	if err := p.QueryRow(ctx, "SELECT count(*),min(checksum),min(version) FROM media.schema_migrations").Scan(&count, &got, &version); err != nil {
		return err
	}
	if count != 1 || version != 1 || got != checksum {
		return ErrConfiguration
	}
	rows, err := p.Query(ctx, "SELECT id,state,revision,filename,mime,size,width,height,sha256,created_at FROM media.assets WHERE false")
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// Migrate explicitly installs immutable PostgreSQL metadata; Start never does DDL.
// A source rollback does not reverse this ledger or durable filesystem effects.
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
	if err = tx.QueryRow(ctx, "SELECT to_regnamespace('media') IS NOT NULL").Scan(&exists); err != nil {
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
	if _, err = tx.Exec(ctx, "INSERT INTO media.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
		return ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnknownOutcome
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
func lockAsset(ctx context.Context, p *pgxpool.Pool, id string, shared bool) (*pgxpool.Conn, func(), error) {
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
			_ = conn.Close(cleanup)
			cancel()
		} else {
			c.Release()
		}
		if err == nil {
			err = ErrConflict
		}
		return nil, nil, err
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		var released bool
		if err := c.QueryRow(cleanup, "SELECT "+unlock+"($1)", lockKey(id)).Scan(&released); err != nil || !released {
			conn := c.Hijack()
			_ = conn.Close(cleanup)
		} else {
			c.Release()
		}
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
