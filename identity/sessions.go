// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (m *Module) findCredential(parent context.Context, kind, value string) (credential, error) {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return credential{}, err
	}
	defer finish()
	sql := "SELECT a.id,a.login,a.enabled,a.revision,c.password_hash FROM identity.accounts a JOIN identity.credentials c ON c.account_id=a.id WHERE a.login=$1"
	if kind == "id" {
		sql = "SELECT a.id,a.login,a.enabled,a.revision,c.password_hash FROM identity.accounts a JOIN identity.credentials c ON c.account_id=a.id WHERE a.id=$1"
	}
	var c credential
	err = p.QueryRow(ctx, sql, value).Scan(&c.ID, &c.Login, &c.Enabled, &c.Revision, &c.hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrAuthentication
	}
	if err != nil {
		return c, m.failure(ctx, "credential_read", err)
	}
	return c, nil
}
func lockAccount(ctx context.Context, tx pgx.Tx, id string, revision int64, enabled bool) error {
	var got int64
	var active bool
	err := tx.QueryRow(ctx, "SELECT revision,enabled FROM identity.accounts WHERE id=$1 FOR UPDATE", id).Scan(&got, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthentication
	}
	if err != nil {
		return err
	}
	if got != revision || (enabled && !active) {
		return ErrAuthentication
	}
	return nil
}
func insertSession(ctx context.Context, tx pgx.Tx, record sessionRecord, now time.Time) error {
	_, err := tx.Exec(ctx, "INSERT INTO identity.sessions(token_hash,account_id,account_revision,csrf_hash,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)", record.tokenHash, record.accountID, record.revision, record.csrfHash, record.expires, now)
	return err
}
func (m *Module) issue(ctx context.Context, c credential, record sessionRecord, rehash string, previous []byte, now time.Time) error {
	return m.transaction(ctx, "session_issue", func(ctx context.Context, tx pgx.Tx) error {
		if err := lockAccount(ctx, tx, c.ID, c.Revision, true); err != nil {
			return err
		}
		// Hash verification happens outside the lock. Revision fences concurrent
		// password/status/revoke-all changes before this session can be committed.
		if rehash != "" {
			if _, err := tx.Exec(ctx, "UPDATE identity.credentials SET password_hash=$2 WHERE account_id=$1", c.ID, rehash); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM identity.sessions WHERE account_id=$1 AND expires_at<=$2", c.ID, now); err != nil {
			return err
		}
		if len(previous) == 32 {
			if _, err := tx.Exec(ctx, "DELETE FROM identity.sessions WHERE token_hash=$1", previous); err != nil {
				return err
			}
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM identity.sessions WHERE account_id=$1", c.ID).Scan(&count); err != nil {
			return err
		}
		if count >= 16 {
			return ErrLimited
		}
		return insertSession(ctx, tx, record, now)
	})
}
func (m *Module) lookup(parent context.Context, hash []byte, now time.Time) (sessionRecord, error) {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return sessionRecord{}, err
	}
	defer finish()
	record := sessionRecord{tokenHash: hash}
	err = p.QueryRow(ctx, "SELECT s.account_id,s.account_revision,s.csrf_hash,s.expires_at FROM identity.sessions s JOIN identity.accounts a ON a.id=s.account_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND a.enabled AND a.revision=s.account_revision", hash, now).Scan(&record.accountID, &record.revision, &record.csrfHash, &record.expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionRecord{}, ErrAuthentication
	}
	if err != nil {
		return sessionRecord{}, m.failure(ctx, "session_read", err)
	}
	record.expires = record.expires.UTC()
	return record, nil
}
func (m *Module) rotate(ctx context.Context, old []byte, next sessionRecord, now time.Time) error {
	return m.transaction(ctx, "session_rotate", func(ctx context.Context, tx pgx.Tx) error {
		if err := lockAccount(ctx, tx, next.accountID, next.revision, true); err != nil {
			return err
		}
		var expiry time.Time
		err := tx.QueryRow(ctx, "DELETE FROM identity.sessions WHERE token_hash=$1 AND account_id=$2 AND account_revision=$3 AND expires_at>$4 RETURNING expires_at", old, next.accountID, next.revision, now).Scan(&expiry)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAuthentication
		}
		if err != nil {
			return err
		}
		// A stale reader may never extend the source session's absolute deadline.
		if !expiry.Equal(next.expires) {
			return ErrAuthentication
		}
		return insertSession(ctx, tx, next, now)
	})
}
func (m *Module) revoke(parent context.Context, hash []byte) error {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	_, err = p.Exec(ctx, "DELETE FROM identity.sessions WHERE token_hash=$1", hash)
	if err != nil {
		return m.failure(ctx, "session_revoke", err)
	}
	return nil
}
func (m *Module) refreshCSRF(ctx context.Context, hash, csrf []byte, now time.Time) error {
	return m.transaction(ctx, "csrf_refresh", func(ctx context.Context, tx pgx.Tx) error {
		var id string
		var revision int64
		err := tx.QueryRow(ctx, "SELECT account_id,account_revision FROM identity.sessions WHERE token_hash=$1", hash).Scan(&id, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAuthentication
		}
		if err != nil {
			return err
		}
		if err = lockAccount(ctx, tx, id, revision, true); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "UPDATE identity.sessions SET csrf_hash=$2 WHERE token_hash=$1 AND expires_at>$3", hash, csrf, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAuthentication
		}
		return nil
	})
}
