// SPDX-License-Identifier: MPL-2.0
// Package audit owns retained accountability records. Domains choose accountable
// actions; operational logs and repeated public denials are not Audit events.
package audit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
)

const (
	ModuleVersion = "0.2.0-development"
	Append        = "achrix.audit.append"
	Query         = "achrix.audit.query"
	Export        = "achrix.audit.export"
)

var (
	ErrConfiguration = errors.New("invalid audit configuration")
	ErrInput         = errors.New("invalid audit input")
	ErrUnavailable   = errors.New("audit unavailable")
	ErrConflict      = errors.New("audit preparation already consumed")
	ErrLimited       = errors.New("audit capacity exceeded")
)

// Config.Now is a trusted, concurrent-safe product time source. Records store UTC
// microsecond instants; it conveys no trusted timestamp or clock attestation.
type Config struct{ Now func() time.Time }

// Event deliberately has no caller-selected actor, time, ID or arbitrary payload.
// Authority identifies the owning domain capability, not an authorization grant.
type Event struct{ Action, Target, Authority, Outcome string }

type Record struct {
	ID         string
	Actor      achrix.Principal
	Action     string
	Target     string
	Authority  string
	Outcome    string
	OccurredAt time.Time
}

type Page struct {
	Records    []Record
	NextCursor string
}

type preparedState struct {
	module  *Module
	context context.Context
	record  Record
	used    atomic.Bool
}

// Prepared is opaque, single-use and bound to the original live operation and
// Audit Module. It cannot extend a deadline or be retained as an access grant.
type Prepared struct{ state *preparedState }

type Service struct {
	app    *achrix.Application
	module *Module
}

func NewService(app *achrix.Application, module *Module) (*Service, error) {
	if app == nil || module == nil {
		return nil, ErrConfiguration
	}
	return &Service{app: app, module: module}, nil
}

var reference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var name = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

func validReference(v string) bool { return len(v) > 0 && len(v) <= 128 && reference.MatchString(v) }
func validName(v string) bool      { return len(v) > 0 && len(v) <= 96 && name.MatchString(v) }

func (s *Service) Prepare(ctx context.Context, actor achrix.Principal, event Event) (Prepared, error) {
	if !validReference(string(actor)) || !validReference(event.Target) || !validName(event.Action) || !validName(event.Authority) || event.Outcome != "succeeded" {
		return Prepared{}, ErrInput
	}
	if err := s.app.Authorize(ctx, actor, Append, event.Target); err != nil {
		return Prepared{}, err
	}
	if err := s.module.admission(ctx); err != nil {
		return Prepared{}, err
	}
	now := s.module.now()
	if now.Year() < 1 || now.Year() > 9999 {
		return Prepared{}, ErrConfiguration
	}
	r := Record{ID: strings.ToLower(rand.Text()), Actor: actor, Action: event.Action, Target: event.Target, Authority: event.Authority, Outcome: event.Outcome, OccurredAt: now}
	return Prepared{&preparedState{module: s.module, context: ctx, record: r}}, nil
}

// CheckDatabase prevents a same-transaction consumer from silently composing
// different database profiles. Equivalent parsed connection settings match;
// passwords, TLS trust and fallback settings are compared without being exposed.
func (s *Service) CheckDatabase(dsn string) error {
	c, err := databaseConfig(dsn)
	if err != nil || !sameDatabase(s.module.dbConfig.ConnConfig, c.ConnConfig) {
		return ErrConfiguration
	}
	return nil
}

// AppendInTx owns Audit SQL while the caller owns the native PostgreSQL
// transaction and commit. The domain must roll back if this call fails. A
// successful return alone does not prove either write has committed.
func AppendInTx(parent context.Context, tx pgx.Tx, prepared Prepared) error {
	p := prepared.state
	if p == nil || p.module == nil || tx == nil || tx.Conn() == nil {
		return ErrInput
	}
	if err := p.context.Err(); err != nil {
		return err
	}
	deadline, ok := p.context.Deadline()
	if !ok {
		return ErrConfiguration
	}
	if !sameDatabase(p.module.dbConfig.ConnConfig, tx.Conn().Config()) {
		return ErrConfiguration
	}
	ctx, _, finish, err := p.module.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(p.context, cancel)
	defer func() { stop(); cancel() }()
	if err = p.context.Err(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !p.used.CompareAndSwap(false, true) {
		return ErrConflict
	}
	r := p.record
	_, err = tx.Exec(ctx, "INSERT INTO audit.records(id,actor,action,target,authority,outcome,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)", r.ID, string(r.Actor), r.Action, r.Target, r.Authority, r.Outcome, r.OccurredAt)
	if err != nil {
		return p.module.failure(ctx, "append", err)
	}
	return ctx.Err()
}

// Append commits an authorized standalone record. Atomic domain actions instead
// use Prepare/AppendInTx within their owning transaction.
func (s *Service) Append(parent context.Context, actor achrix.Principal, event Event) (Record, error) {
	p, err := s.Prepare(parent, actor, event)
	if err != nil {
		return Record{}, err
	}
	err = s.module.transaction(parent, "append", func(ctx context.Context, tx pgx.Tx) error { return AppendInTx(ctx, tx, p) })
	if err != nil {
		return Record{}, err
	}
	return p.state.record, nil
}

// Query and Export require separate permissions on the exact target. Each is a
// bounded page of committed visible records, not a snapshot across requests.
func (s *Service) Query(ctx context.Context, actor achrix.Principal, target, cursor string, limit int) (Page, error) {
	return s.page(ctx, actor, target, cursor, limit, Query)
}
func (s *Service) Export(ctx context.Context, actor achrix.Principal, target, cursor string, limit int) (Page, error) {
	return s.page(ctx, actor, target, cursor, limit, Export)
}
func (s *Service) page(parent context.Context, actor achrix.Principal, target, cursor string, limit int, capability string) (Page, error) {
	if !validReference(string(actor)) || !validReference(target) || limit < 1 || limit > 100 {
		return Page{}, ErrInput
	}
	after, err := decodeCursor(target, cursor)
	if err != nil {
		return Page{}, err
	}
	if err = s.app.Authorize(parent, actor, capability, target); err != nil {
		return Page{}, err
	}
	ctx, pool, finish, err := s.module.acquire(parent)
	if err != nil {
		return Page{}, err
	}
	defer finish()
	rows, err := pool.Query(ctx, "SELECT seq,id,actor,action,target,authority,outcome,occurred_at FROM audit.records WHERE target=$1 AND seq>$2 ORDER BY seq LIMIT $3", target, after, limit+1)
	if err != nil {
		return Page{}, s.module.failure(ctx, "query", err)
	}
	defer rows.Close()
	page := Page{Records: make([]Record, 0, limit)}
	var last int64
	for rows.Next() {
		var seq int64
		var r Record
		if err = rows.Scan(&seq, &r.ID, &r.Actor, &r.Action, &r.Target, &r.Authority, &r.Outcome, &r.OccurredAt); err != nil {
			return Page{}, s.module.failure(ctx, "query", err)
		}
		if len(page.Records) == limit {
			page.NextCursor = encodeCursor(target, last)
			break
		}
		r.OccurredAt = r.OccurredAt.UTC()
		page.Records = append(page.Records, r)
		last = seq
	}
	if err = rows.Err(); err != nil {
		return Page{}, s.module.failure(ctx, "query", err)
	}
	return page, nil
}

func encodeCursor(target string, seq int64) string {
	var b [25]byte
	b[0] = 1
	binary.BigEndian.PutUint64(b[1:9], uint64(seq))
	sum := sha256.Sum256([]byte(target))
	copy(b[9:], sum[:16])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func decodeCursor(target, cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) != 34 {
		return 0, ErrInput
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(b) != 25 || b[0] != 1 || base64.RawURLEncoding.EncodeToString(b) != cursor {
		return 0, ErrInput
	}
	seq := int64(binary.BigEndian.Uint64(b[1:9]))
	if seq <= 0 || encodeCursor(target, seq) != cursor {
		return 0, ErrInput
	}
	return seq, nil
}
