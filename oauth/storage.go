// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hash(value string) []byte {
	h := sha256.Sum256([]byte(value))
	return h[:]
}

func validToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(value)
	return e == nil && len(b) == 32
}

func (s *Server) now() time.Time { return s.config.Now().UTC().Truncate(time.Microsecond) }

func (s *Server) report(ctx context.Context, phase, status string, err error) {
	s.config.Diagnostic(ctx, phase, status, err)
}

// Migrate explicitly installs a versioned transactional PostgreSQL schema.
// Concurrent migration attempts serialize; unknown versions or changed source
// hashes fail closed. Retained grants/revocations require product-wide backup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return ErrConfiguration
	}
	source, err := migrations.ReadFile("migrations/001_oauth.sql")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(source)
	checksum := hex.EncodeToString(digest[:])
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin oauth migration: %w", err)
	}
	defer tx.Rollback(ctx)
	var serverVersion int
	var encoding string
	if err = tx.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer, current_setting('server_encoding')").Scan(&serverVersion, &encoding); err != nil {
		return fmt.Errorf("check OAuth PostgreSQL environment: %w", err)
	}
	if serverVersion != 180006 || encoding != "UTF8" {
		return fmt.Errorf("%w: OAuth requires PostgreSQL 18.6 UTF8", ErrConfiguration)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1929384711)"); err != nil {
		return fmt.Errorf("lock oauth migration: %w", err)
	}
	if _, err = tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS oauth"); err != nil {
		return fmt.Errorf("create oauth schema: %w", err)
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS oauth.schema_migrations (version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("create oauth migration ledger: %w", err)
	}
	rows, err := tx.Query(ctx, "SELECT version, checksum FROM oauth.schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read oauth migration ledger: %w", err)
	}
	count := 0
	for rows.Next() {
		var version int
		var stored string
		if err = rows.Scan(&version, &stored); err != nil {
			break
		}
		if version != 1 || stored != checksum {
			err = fmt.Errorf("oauth migration version/checksum mismatch")
			break
		}
		count++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if count > 1 {
		return errors.New("oauth migration ledger duplicate version")
	}
	if count == 0 {
		if _, err = tx.Exec(ctx, string(source)); err != nil {
			return fmt.Errorf("apply oauth migration: %w", err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO oauth.schema_migrations(version,checksum) VALUES(1,$1)", checksum); err != nil {
			return fmt.Errorf("record oauth migration: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("oauth migration commit outcome requires reconciliation: %w", err)
	}
	return nil
}

// Prune removes expired one-use and token rows, then retains orphan grants
// for a 30-day reconciliation window after their last grant/revocation event.
// Active grant/token relationships are never deleted. Refresh lifetime is
// absolute (not sliding), so replay evidence remains until that expiry.
// Operators must call Prune on a bounded cadence; no background task starts.
func (s *Server) Prune(ctx context.Context) error {
	if s == nil {
		return ErrConfiguration
	}
	now := s.now()
	tx, err := s.config.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: prune begin: %v", ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		"DELETE FROM oauth.ceremonies WHERE expires_at < $1",
		"DELETE FROM oauth.codes WHERE expires_at < $1",
		"DELETE FROM oauth.access_tokens WHERE expires_at < $1",
		"DELETE FROM oauth.refresh_tokens WHERE expires_at < $1",
	} {
		if _, err = tx.Exec(ctx, q, now); err != nil {
			return fmt.Errorf("%w: prune: %v", ErrUnavailable, err)
		}
	}
	// The existing PostgreSQL FK design prevents an accidental cascade of
	// active credentials. Only remove a grant when *all* its code/access/refresh
	// rows are gone; no outstanding credential can be reanimated by cleanup.
	_, err = tx.Exec(ctx, `DELETE FROM oauth.grants g WHERE
       COALESCE(g.revoked_at,g.created_at) < $1
       AND NOT EXISTS(SELECT 1 FROM oauth.codes c WHERE c.grant_id=g.id)
       AND NOT EXISTS(SELECT 1 FROM oauth.access_tokens a WHERE a.grant_id=g.id)
       AND NOT EXISTS(SELECT 1 FROM oauth.refresh_tokens r WHERE r.grant_id=g.id)`, now.Add(-30*24*time.Hour))
	if err != nil {
		return fmt.Errorf("%w: prune orphan grants: %v", ErrUnavailable, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: prune outcome unknown: %v", ErrUnavailable, err)
	}
	return nil
}

func (s *Server) newGrant(subject, clientID string, scopes []string, now time.Time) (Grant, error) {
	id, err := token()
	if err != nil {
		return Grant{}, err
	}
	return Grant{ID: id, Subject: subject, ClientID: clientID, Issuer: s.config.Issuer, Resource: s.config.Resource, Scopes: append([]string(nil), scopes...), CreatedAt: now}, nil
}

func (s *Server) active(ctx context.Context, g Grant) error {
	if g.Subject == "" || g.ClientID == "" || g.Resource != s.config.Resource || g.Issuer != s.config.Issuer {
		return ErrInvalidGrant
	}
	c, err := s.client(ctx, g.ClientID)
	if err != nil {
		return err
	}
	for _, scope := range g.Scopes {
		if !slices.Contains(c.Scopes, scope) {
			return ErrInvalidGrant
		}
	}
	if err := s.config.Permit(ctx, g); err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			return ErrInvalidGrant
		}
		return fmt.Errorf("%w: current product authority: %v", ErrUnavailable, err)
	}
	return nil
}
