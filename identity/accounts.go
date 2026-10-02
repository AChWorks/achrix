// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (m *Module) account(parent context.Context, id string) (Account, error) {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return Account{}, err
	}
	defer finish()
	var a Account
	err = p.QueryRow(ctx, "SELECT id,login,enabled,revision FROM identity.accounts WHERE id=$1", id).Scan(&a.ID, &a.Login, &a.Enabled, &a.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrConflict
	}
	if err != nil {
		return Account{}, m.failure(ctx, "account_read", err)
	}
	return a, nil
}

func (m *Module) lookupAccount(parent context.Context, login string) (Account, error) {
	ctx, p, finish, err := m.acquire(parent)
	if err != nil {
		return Account{}, err
	}
	defer finish()
	var a Account
	// Existing UNIQUE(login) owns the bounded exact predicate. Do not join
	// credentials or sessions into this metadata-only reconciliation path.
	err = p.QueryRow(ctx, "SELECT id,login,enabled,revision FROM identity.accounts WHERE login=$1", login).Scan(&a.ID, &a.Login, &a.Enabled, &a.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, m.failure(ctx, "account_lookup", err)
	}
	return a, nil
}

func (m *Module) create(parent context.Context, a Account, hash string, now time.Time, prepared audit.Prepared) error {
	return m.transaction(parent, "account_create", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO identity.accounts(id,login,enabled,revision,created_at) VALUES($1,$2,$3,$4,$5)", a.ID, a.Login, a.Enabled, a.Revision, now)
		if err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return ErrConflict
			}
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO identity.credentials(account_id,password_hash,changed_at) VALUES($1,$2,$3)", a.ID, hash, now); err != nil {
			return err
		}
		return audit.AppendInTx(ctx, tx, prepared)
	})
}

// lockManagedAccount serializes every credential/status/revoke-all change with
// session issuance. Revision zero means the management operation has no caller
// revision precondition; it still advances the locked revision exactly once.
func lockManagedAccount(ctx context.Context, tx pgx.Tx, id string, expected int64) (int64, error) {
	var revision int64
	err := tx.QueryRow(ctx, "SELECT revision FROM identity.accounts WHERE id=$1 FOR UPDATE", id).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	if expected != 0 && revision != expected {
		return 0, ErrConflict
	}
	if revision == math.MaxInt64 {
		return 0, ErrConflict
	}
	return revision + 1, nil
}

func replacePassword(ctx context.Context, tx pgx.Tx, id string, revision int64, hash string, now time.Time) error {
	tag, err := tx.Exec(ctx, "UPDATE identity.credentials SET password_hash=$2,changed_at=$3 WHERE account_id=$1", id, hash, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE identity.accounts SET revision=$2 WHERE id=$1", id, revision); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "DELETE FROM identity.sessions WHERE account_id=$1", id)
	return err
}

func (m *Module) setPassword(parent context.Context, id string, expectedRevision int64, hash string, now time.Time, prepared audit.Prepared) error {
	return m.transaction(parent, "credential_set", func(ctx context.Context, tx pgx.Tx) error {
		revision, err := lockManagedAccount(ctx, tx, id, expectedRevision)
		if err != nil {
			return err
		}
		if err = replacePassword(ctx, tx, id, revision, hash, now); err != nil {
			return err
		}
		return audit.AppendInTx(ctx, tx, prepared)
	})
}

func (m *Module) setEnabled(parent context.Context, id string, expectedRevision int64, enabled bool, now time.Time, prepared audit.Prepared) error {
	return m.transaction(parent, "account_status", func(ctx context.Context, tx pgx.Tx) error {
		revision, err := lockManagedAccount(ctx, tx, id, expectedRevision)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE identity.accounts SET enabled=$2,revision=$3 WHERE id=$1", id, enabled, revision); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM identity.sessions WHERE account_id=$1", id); err != nil {
			return err
		}
		return audit.AppendInTx(ctx, tx, prepared)
	})
}

func (m *Module) revokeAll(parent context.Context, id string, now time.Time, prepared audit.Prepared) error {
	return m.transaction(parent, "session_revoke_all", func(ctx context.Context, tx pgx.Tx) error {
		revision, err := lockManagedAccount(ctx, tx, id, 0)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE identity.accounts SET revision=$2 WHERE id=$1", id, revision); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM identity.sessions WHERE account_id=$1", id); err != nil {
			return err
		}
		return audit.AppendInTx(ctx, tx, prepared)
	})
}

func (m *Module) changePassword(parent context.Context, value string, p achrix.Principal, revision int64, hash string, now time.Time, prepared audit.Prepared) error {
	token, err := tokenHash(value)
	if err != nil {
		return err
	}
	return m.transaction(parent, "password_change", func(ctx context.Context, tx pgx.Tx) error {
		if err := lockAccount(ctx, tx, string(p), revision, true); err != nil {
			return err
		}
		// Waiting for the account fence may outlive the initiating session.
		// Re-sample trusted time after the lock before authorizing this mutation.
		now = m.now()
		if revision == math.MaxInt64 {
			return ErrConflict
		}
		// The exact initiating token must still be live after taking the account
		// fence, even if it was rotated/revoked while password verification ran.
		var found bool
		err := tx.QueryRow(ctx, "SELECT true FROM identity.sessions WHERE token_hash=$1 AND account_id=$2 AND account_revision=$3 AND expires_at>$4 FOR UPDATE", token, string(p), revision, now).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAuthentication
		}
		if err != nil {
			return err
		}
		if err = replacePassword(ctx, tx, string(p), revision+1, hash, now); err != nil {
			return err
		}
		return audit.AppendInTx(ctx, tx, prepared)
	})
}
