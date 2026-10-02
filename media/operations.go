// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Explicit SQL errors prove rejection; transport/context failures around an
// autocommit mutation can have lost an acknowledgement. Do not interpret them as
// rollback or authorize filesystem compensation without confirmed durable intent.
func mutationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Severity == "ERROR" {
		return ErrUnavailable
	}
	return errors.Join(ErrUnknownOutcome, errContext(err))
}
func errContext(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
func (m *Module) create(parent context.Context, filename string, input io.Reader) (Asset, error) {
	ctx, p, st, finish, err := m.acquire(parent)
	if err != nil {
		return Asset{}, err
	}
	defer finish()
	storageRelease, err := st.lock(true)
	if err != nil {
		return Asset{}, m.failure(ctx, "storage_lock", err)
	}
	defer storageRelease()
	a := Asset{ID: newID(), Filename: filename, State: "pending", Revision: 1, CreatedAt: m.config.Now().UTC().Truncate(time.Microsecond)}
	if a.CreatedAt.Year() < 1 || a.CreatedAt.Year() > 9999 {
		return Asset{}, ErrConfiguration
	}
	c, release, err := lockAsset(ctx, p, a.ID, false)
	if err != nil {
		return Asset{}, m.failure(ctx, "create_lock", err)
	}
	defer release()
	if err = st.unused(a.ID); err != nil {
		return Asset{}, m.failure(ctx, "create_storage_identity", err)
	}
	var id string
	err = c.QueryRow(ctx, "INSERT INTO media.assets(id,state,revision,filename,mime,size,width,height,sha256,created_at) VALUES($1,'pending',1,$2,'',0,0,0,'',$3) RETURNING id", a.ID, a.Filename, a.CreatedAt).Scan(&id)
	if err != nil {
		return a, m.failure(ctx, "create_intent", mutationError(err))
	}
	a, err = st.write(ctx, m, a, input)
	if err != nil {
		if errors.Is(err, ErrUnknownOutcome) {
			return a, m.failure(ctx, "create_storage", err)
		}
		// This connection acknowledged pending intent. Cleanup first confirms deleting
		// in a fresh bounded context; cancellation never invents an acknowledged state.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, cleanupErr := m.cleanup(cleanup, c, st, a.ID)
		if cleanupErr != nil {
			return a, errors.Join(m.failure(ctx, "create_input", err), m.failure(cleanup, "create_cleanup", cleanupErr))
		}
		return a, m.failure(ctx, "create_input", err)
	}
	err = c.QueryRow(ctx, "UPDATE media.assets SET state='ready',revision=2,mime=$2,size=$3,width=$4,height=$5,sha256=$6 WHERE id=$1 AND state='pending' AND revision=1 RETURNING "+assetColumns, a.ID, a.MIME, a.Size, a.Width, a.Height, a.SHA256).Scan(&a.ID, &a.State, &a.Revision, &a.Filename, &a.MIME, &a.Size, &a.Width, &a.Height, &a.SHA256, &a.CreatedAt)
	if err != nil {
		return a, m.failure(ctx, "create_publish", mutationError(err))
	}
	return a, nil
}
func (m *Module) read(parent context.Context, id string, dst io.Writer) (Asset, error) {
	ctx, p, st, finish, err := m.acquire(parent)
	if err != nil {
		return Asset{}, err
	}
	defer finish()
	storageRelease, err := st.lock(true)
	if err != nil {
		return Asset{}, m.failure(ctx, "storage_lock", err)
	}
	defer storageRelease()
	c, release, err := lockAsset(ctx, p, id, true)
	if err != nil {
		return Asset{}, m.failure(ctx, "read_lock", err)
	}
	defer release()
	a, err := findAsset(ctx, c, id)
	if err != nil {
		return Asset{}, m.failure(ctx, "read", err)
	}
	if a.State != "ready" {
		return a, ErrNotFound
	}
	f, err := st.open(id)
	if err != nil {
		return a, m.failure(ctx, "read_storage", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() != a.Size {
		return a, m.failure(ctx, "read_size", ErrUnavailable)
	}
	h := sha256.New()
	n, err := copyBounded(ctx, h, f, MaxUploadBytes+1)
	if err != nil {
		return a, m.failure(ctx, "read_verify", err)
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return a, m.failure(ctx, "read_integrity", ErrUnavailable)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return a, m.failure(ctx, "read_seek", err)
	}
	n, err = copyBounded(ctx, dst, f, a.Size)
	if err != nil {
		return a, m.failure(ctx, "read_copy", err)
	}
	if n != a.Size {
		return a, ErrUnavailable
	}
	return a, ctx.Err()
}
func (m *Module) delete(parent context.Context, id string, expected int64) error {
	ctx, p, st, finish, err := m.acquire(parent)
	if err != nil {
		return err
	}
	defer finish()
	storageRelease, err := st.lock(false)
	if err != nil {
		return m.failure(ctx, "storage_lock", err)
	}
	defer storageRelease()
	c, release, err := lockAsset(ctx, p, id, false)
	if err != nil {
		return m.failure(ctx, "delete_lock", err)
	}
	defer release()
	a, err := findAsset(ctx, c, id)
	if err != nil {
		return m.failure(ctx, "delete", err)
	}
	if a.State != "ready" || a.Revision != expected || expected >= maxRevision-1 {
		return ErrConflict
	}
	var revision int64
	err = c.QueryRow(ctx, "UPDATE media.assets SET state='deleting',revision=revision+1 WHERE id=$1 AND state='ready' AND revision=$2 RETURNING revision", id, expected).Scan(&revision)
	if err != nil {
		return m.failure(ctx, "delete_intent", mutationError(err))
	}
	_, err = m.cleanup(ctx, c, st, id)
	if err != nil {
		return m.failure(ctx, "delete_finish", err)
	}
	return nil
}
func (m *Module) cleanup(ctx context.Context, c *pgxpool.Conn, st *localStorage, id string) (bool, error) {
	a, err := findAsset(ctx, c, id)
	if err != nil {
		return false, err
	}
	switch a.State {
	case "ready", "deleted":
		return false, nil
	case "pending":
		// Both deleting and deleted need a fresh revision. Reject before any
		// state/file change when the durable integer cannot represent them.
		if a.Revision >= maxRevision-1 {
			return false, ErrConflict
		}
		err = c.QueryRow(ctx, "UPDATE media.assets SET state='deleting',revision=revision+1 WHERE id=$1 AND state='pending' AND revision=$2 RETURNING revision", id, a.Revision).Scan(&a.Revision)
		if err != nil {
			return false, mutationError(err)
		}
	case "deleting":
		if a.Revision >= maxRevision {
			return false, ErrConflict
		}
	default:
		return false, ErrUnavailable
	}
	// The deleting intent was read/acknowledged under this asset's exclusive lock.
	// File deletion is idempotent; missing files are normal after interrupted unlink.
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if err = st.remove(id); err != nil {
		return false, err
	}
	err = c.QueryRow(ctx, "UPDATE media.assets SET state='deleted',revision=revision+1,filename='',mime='',size=0,width=0,height=0,sha256='' WHERE id=$1 AND state='deleting' AND revision=$2 RETURNING revision", id, a.Revision).Scan(&a.Revision)
	if err != nil {
		return false, mutationError(err)
	}
	return true, nil
}
func (m *Module) reconcile(parent context.Context, limit int) (ReconcileResult, error) {
	ctx, p, st, finish, err := m.acquire(parent)
	if err != nil {
		return ReconcileResult{}, err
	}
	defer finish()
	storageRelease, err := st.lock(false)
	if err != nil {
		return ReconcileResult{}, m.failure(ctx, "storage_lock", err)
	}
	defer storageRelease()
	rows, err := p.Query(ctx, "SELECT id FROM media.assets WHERE state IN ('pending','deleting') ORDER BY id LIMIT $1", limit)
	if err != nil {
		return ReconcileResult{}, m.failure(ctx, "reconcile_list", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return ReconcileResult{}, m.failure(ctx, "reconcile_list", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ReconcileResult{}, m.failure(ctx, "reconcile_list", err)
	}
	result := ReconcileResult{}
	for _, id := range ids {
		c, release, err := lockAsset(ctx, p, id, false)
		if errors.Is(err, ErrConflict) {
			result.Busy++
			continue
		}
		if err != nil {
			return result, m.failure(ctx, "reconcile_lock", err)
		}
		changed, err := m.cleanup(ctx, c, st, id)
		release()
		if err != nil {
			return result, m.failure(ctx, "reconcile_finish", err)
		}
		if changed {
			result.Processed++
		}
	}
	return result, ctx.Err()
}
