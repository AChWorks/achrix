// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
)

const testActor achrix.Principal = "media-test-manager"

type fixture struct {
	dsn     string
	db      *pgx.Conn
	module  *Module
	service *Service
	app     *achrix.Application
	root    string
}

func newFixture(t *testing.T, allowed ...[]string) *fixture {
	t.Helper()
	config := Config{}
	if len(allowed) != 0 {
		config.AllowedMIMEs = allowed[0]
	}
	return newFixtureConfig(t, config)
}
func newFixtureConfig(t *testing.T, config Config) *fixture {
	t.Helper()
	dsn := os.Getenv("ACHRIX_MEDIA_TEST_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_MEDIA_TEST_DSN absent: real PostgreSQL Media proof not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.Database, "achrix_media_") {
		t.Fatal("requires disposable achrix_media_ DB")
	}
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot connect private Media proof DB")
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = db.Close(c)
	})
	if _, err = db.Exec(ctx, "DROP SCHEMA IF EXISTS media CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	config.StorageRoot = root
	m, err := NewPostgres(dsn, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, c, r string) error {
		if p == publicImageActor && c == PreparePublicImage && validID(r) {
			return nil
		}
		if p != testActor {
			return achrix.ErrDenied
		}
		switch c {
		case Create, List, Reconcile:
			if r == LibraryTarget {
				return nil
			}
		case Read, Delete:
			if validID(r) {
				return nil
			}
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, m)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(c); err != nil {
			t.Error(err)
		}
	})
	service, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{dsn, db, m, service, app, root}
}
func operationContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 10*time.Second)
}
func TestPostgresLifecycleAuthorizationAndPagination(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	data := imageBytes(t, "png", 4, 3)
	if _, err := f.service.Create(ctx, "denied", "x.png", bytes.NewReader(data)); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denied create")
	}
	var count int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM media.assets").Scan(&count); err != nil || count != 0 {
		t.Fatal("denied metadata mutation")
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("denied storage mutation")
	}
	assets := make([]Asset, 3)
	for i := range assets {
		assets[i], err = f.service.Create(ctx, testActor, "تصویر\u200cنمونه.png", bytes.NewReader(data))
		if err != nil || assets[i].State != "ready" || assets[i].Revision != 2 || assets[i].Filename != "تصویر\u200cنمونه.png" || assets[i].CreatedAt.Location() != time.UTC {
			t.Fatalf("create %+v %v", assets[i], err)
		}
	}
	var output bytes.Buffer
	a, err := f.service.Read(ctx, testActor, assets[0].ID, &output)
	if err != nil || !bytes.Equal(output.Bytes(), data) || a.Width != 4 || a.Height != 3 {
		t.Fatal("read bytes/metadata", err)
	}
	output.Reset()
	if _, err = f.service.Read(ctx, "denied", a.ID, &output); !errors.Is(err, achrix.ErrDenied) || output.Len() != 0 {
		t.Fatal("denied bytes")
	}
	if _, err = f.service.Status(ctx, "denied", a.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denied metadata")
	}
	if err = f.service.Delete(ctx, "denied", a.ID, a.Revision); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denied delete")
	}
	if _, err = f.service.Reconcile(ctx, "denied", 10); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denied reconciliation")
	}
	if err = f.service.Delete(ctx, testActor, a.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete")
	}
	p, err := f.service.List(ctx, testActor, "", 2)
	if err != nil || len(p.Assets) != 2 || p.NextCursor == "" {
		t.Fatal("first page", err)
	}
	q, err := f.service.List(ctx, testActor, p.NextCursor, 2)
	if err != nil || len(q.Assets) != 1 || q.NextCursor != "" || q.Assets[0].ID <= p.Assets[1].ID {
		t.Fatal("keyset page", err)
	}
	if err = f.service.Delete(ctx, testActor, a.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	gone, err := f.service.Status(ctx, testActor, a.ID)
	if err != nil || gone.State != "deleted" || gone.Revision != 4 || gone.Filename != "" || gone.SHA256 != "" {
		t.Fatalf("tombstone %+v %v", gone, err)
	}
	output.Reset()
	if _, err = f.service.Read(ctx, testActor, a.ID, &output); !errors.Is(err, ErrNotFound) || output.Len() != 0 {
		t.Fatal("deleted bytes visible")
	}
	if err = f.service.Delete(ctx, testActor, a.ID, a.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("blind delete repeated")
	}
	if _, err = f.service.Create(ctx, testActor, "x.jpg", bytes.NewReader(imageBytes(t, "jpeg", 8, 8))); err != nil {
		t.Fatal("JPEG", err)
	}
}
func TestInterruptedUploadDeleteAndIdempotentReconciliation(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	ready, err := f.service.Create(ctx, testActor, "kept.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	pending, deleting := newID(), newID()
	for _, v := range []struct{ id, state string }{{pending, "pending"}, {deleting, "deleting"}} {
		if _, err = f.db.Exec(ctx, "INSERT INTO media.assets(id,state,revision,filename,mime,size,width,height,sha256,created_at) VALUES($1,$2,1,'interrupted','','0',0,0,'',now())", v.id, v.state); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(f.root+"/"+v.id+".upload", []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(f.root+"/"+v.id, []byte("unpublished"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.service.Reconcile(ctx, testActor, 1)
	if err != nil || result.Processed != 1 {
		t.Fatal("bounded reconcile", result, err)
	}
	result, err = f.service.Reconcile(ctx, testActor, 40)
	if err != nil || result.Processed != 1 {
		t.Fatal("finish reconcile", result, err)
	}
	result, err = f.service.Reconcile(ctx, testActor, 40)
	if err != nil || result.Processed != 0 {
		t.Fatal("repeat reconcile", result, err)
	}
	for _, id := range []string{pending, deleting} {
		a, err := f.service.Status(ctx, testActor, id)
		if err != nil || a.State != "deleted" {
			t.Fatal("not reconciled", err)
		}
		for _, name := range []string{id, id + ".upload"} {
			if _, err = os.Stat(f.root + "/" + name); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("remaining partial")
			}
		}
	}
	var out bytes.Buffer
	if _, err = f.service.Read(ctx, testActor, ready.ID, &out); err != nil {
		t.Fatal("reconcile damaged ready", err)
	}
}
func TestMalformedUploadCleanupAndStorageIntegrity(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	a, err := f.service.Create(ctx, testActor, "bad.png", bytes.NewReader([]byte("invalid")))
	if !errors.Is(err, ErrInput) || !validID(a.ID) {
		t.Fatal("invalid create", a, err)
	}
	status, err := f.service.Status(ctx, testActor, a.ID)
	if err != nil || status.State != "deleted" {
		t.Fatal("failed upload not cleaned", err)
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("partial input file leaked")
	}
	ready, err := f.service.Create(ctx, testActor, "valid.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.root+"/"+ready.ID, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err = f.service.Read(ctx, testActor, ready.ID, &out); !errors.Is(err, ErrUnavailable) || out.Len() != 0 {
		t.Fatal("corrupt bytes returned")
	}
}

type wrappedReadWriter struct{ err error }

func (w wrappedReadWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReadDestinationErrorsAreCanonical(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	asset, err := f.service.Create(ctx, testActor, "destination.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	const private = "private-destination path=/secret password=hidden"
	for _, category := range []error{context.Canceled, context.DeadlineExceeded, ErrInput, ErrConflict, ErrLimited, ErrNotFound, ErrUnavailable, ErrUnknownOutcome} {
		t.Run(category.Error(), func(t *testing.T) {
			_, got := f.service.Read(ctx, testActor, asset.ID, wrappedReadWriter{fmt.Errorf("%s: %w", private, category)})
			if !errors.Is(got, category) || strings.Contains(got.Error(), private) {
				t.Fatalf("private destination error escaped: %v", got)
			}
		})
	}
	_, err = f.service.Read(ctx, testActor, asset.ID, wrappedReadWriter{errors.New(private)})
	if err != ErrUnavailable || strings.Contains(err.Error(), private) {
		t.Fatalf("raw destination error escaped: %v", err)
	}
}

func TestUnknownPublicationAcknowledgement(t *testing.T) {
	f := newFixtureConfig(t, Config{MaxConns: 6, MaxOperations: 8})
	ctx, cancel := operationContext(t)
	defer cancel()
	if _, err := f.db.Exec(ctx, "CREATE FUNCTION media.lose_ready_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='ready' THEN PERFORM pg_terminate_backend(pg_backend_pid()); END IF; RETURN NEW; END $$; CREATE TRIGGER lose_ready_ack BEFORE UPDATE ON media.assets FOR EACH ROW EXECUTE FUNCTION media.lose_ready_ack()"); err != nil {
		t.Fatal(err)
	}
	a, err := f.service.Create(ctx, testActor, "uncertain.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if !errors.Is(err, ErrUnknownOutcome) || !validID(a.ID) {
		t.Fatalf("missing uncertain identity %+v %v", a, err)
	}
	if _, err = os.Stat(f.root + "/" + a.ID); err != nil {
		t.Fatal("unacknowledged publication blindly unlinked")
	}
	status, err := f.service.Status(ctx, testActor, a.ID)
	if err != nil || status.State != "pending" {
		t.Fatal("durable pending missing", status, err)
	}
	result, err := f.service.Reconcile(ctx, testActor, 40)
	if err != nil || result.Processed != 1 {
		t.Fatal("uncertain reconcile", result, err)
	}
}

type blockingReader struct {
	ctx          context.Context
	started      chan struct{}
	continueRead chan struct{}
	once         sync.Once
	r            io.Reader
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-r.continueRead:
		return r.r.Read(p)
	}
}
func TestDatabaseSessionLossCannotRaceActiveFilesystemTransfer(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	reader := &blockingReader{ctx: ctx, started: make(chan struct{}), continueRead: make(chan struct{}), r: bytes.NewReader(imageBytes(t, "png", 2, 2))}
	type result struct {
		a Asset
		e error
	}
	done := make(chan result, 1)
	go func() { a, e := f.service.Create(ctx, testActor, "interrupted.png", reader); done <- result{a, e} }()
	select {
	case <-reader.started:
	case <-ctx.Done():
		t.Fatal("upload not started")
	}
	var pid int
	if err := f.db.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND query LIKE 'INSERT INTO media.assets%RETURNING id' AND state='idle' LIMIT 1").Scan(&pid); err != nil {
		t.Fatal("no idle intent connection", err)
	}
	if _, err := f.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(ctx, testActor, 40); !errors.Is(err, ErrConflict) {
		t.Fatal("cleanup raced transfer after session loss", err)
	}
	close(reader.continueRead)
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("transfer did not finish")
	}
	if !errors.Is(got.e, ErrUnknownOutcome) || !validID(got.a.ID) {
		t.Fatal("sessionloss missing outcome", got.e)
	}
	if _, err := f.service.Reconcile(ctx, testActor, 40); err != nil {
		t.Fatal("reconcile after transfer", err)
	}
}
func TestBoundedAdmissionCancellationAndShutdown(t *testing.T) {
	for _, profile := range []struct {
		name   string
		config Config
	}{
		{"default", Config{}},
		{"minimum", Config{MaxConns: 1, MaxOperations: 1}},
		{"raised", Config{MaxConns: 8, MaxOperations: 8}},
	} {
		t.Run(profile.name, func(t *testing.T) { testBoundedAdmissionCancellationAndShutdown(t, profile.config) })
	}
}
func testBoundedAdmissionCancellationAndShutdown(t *testing.T, config Config) {
	f := newFixtureConfig(t, config)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	readers := make([]*blockingReader, f.module.config.MaxOperations)
	done := make(chan error, len(readers))
	data := imageBytes(t, "png", 2, 2)
	for i := range readers {
		readers[i] = &blockingReader{ctx: parent, started: make(chan struct{}), continueRead: make(chan struct{}), r: bytes.NewReader(data)}
		go func(r *blockingReader) { _, err := f.service.Create(parent, testActor, "blocked.png", r); done <- err }(readers[i])
		select {
		case <-readers[i].started:
		case <-time.After(3 * time.Second):
			t.Fatal("operation not admitted")
		}
	}
	if _, err := f.service.Create(parent, testActor, "over-cap.png", bytes.NewReader(data)); !errors.Is(err, ErrLimited) {
		t.Fatal("unbounded admission", err)
	}
	cancel()
	for range readers {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancel not preserved", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancel did not drain")
		}
	}
	ctx, stop := operationContext(t)
	defer stop()
	if err := f.app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(ctx, testActor, "after.png", bytes.NewReader(data)); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal("admission after shutdown", err)
	}
}
func TestConcurrentDeleteAndMigrationIdentity(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	a, err := f.service.Create(ctx, testActor, "one.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- f.service.Delete(ctx, testActor, a.ID, a.Revision) }()
	}
	successes := 0
	for range 2 {
		err := <-done
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("delete winners", successes)
	}
	if err = Migrate(ctx, f.dsn); err != nil {
		t.Fatal("repeat migration", err)
	}
	if _, err = f.db.Exec(ctx, "UPDATE media.schema_migrations SET checksum=repeat('0',64)"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, f.dsn); !errors.Is(err, ErrUnavailable) {
		t.Fatal("modified migration identity accepted", err)
	}
}

func TestRevisionOverflowRejectsBeforeUnlink(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	data := imageBytes(t, "png", 2, 2)
	ready, err := f.service.Create(ctx, testActor, "retained.png", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(ctx, "UPDATE media.assets SET revision=$2 WHERE id=$1", ready.ID, maxRevision-1); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Delete(ctx, testActor, ready.ID, maxRevision-1); !errors.Is(err, ErrConflict) {
		t.Fatal("overflow delete admitted", err)
	}
	retained, err := os.ReadFile(f.root + "/" + ready.ID)
	if err != nil || !bytes.Equal(retained, data) {
		t.Fatal("overflow delete unlinked data", err)
	}
	for _, state := range []string{"pending", "deleting"} {
		id := newID()
		if _, err = f.db.Exec(ctx, "INSERT INTO media.assets(id,state,revision,filename,mime,size,width,height,sha256,created_at) VALUES($1,$2,$3,'retained','',0,0,0,'',now())", id, state, maxRevision); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(f.root+"/"+id, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.Reconcile(ctx, testActor, 40); !errors.Is(err, ErrConflict) {
			t.Fatal("overflow reconciliation admitted", state, err)
		}
		retained, err = os.ReadFile(f.root + "/" + id)
		if err != nil || !bytes.Equal(retained, data) {
			t.Fatal("overflow reconciliation unlinked data", state, err)
		}
		a, err := f.service.Status(ctx, testActor, id)
		if err != nil || a.State != state || a.Revision != maxRevision {
			t.Fatal("overflow reconciliation mutated state", a, err)
		}
		if _, err = f.db.Exec(ctx, "DELETE FROM media.assets WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.db.Exec(ctx, "UPDATE media.assets SET revision=$2 WHERE id=$1", ready.ID, maxRevision-2); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Delete(ctx, testActor, ready.ID, maxRevision-2); err != nil {
		t.Fatal("representable final revision failed", err)
	}
	a, err := f.service.Status(ctx, testActor, ready.ID)
	if err != nil || a.State != "deleted" || a.Revision != maxRevision {
		t.Fatal("last representable revision", a, err)
	}
}

func TestRealKeysetAndReconciliationQueryPlans(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	rows := make([][]any, 0, 6000)
	for i := 0; i < 6000; i++ {
		state, mime, size, width, height, digest := "ready", "image/png", int64(100), 2, 2, strings.Repeat("0", 64)
		if i%6 == 0 {
			state, mime, size, width, height, digest = "pending", "", 0, 0, 0, ""
		}
		rows = append(rows, []any{newID(), state, int64(1), "synthetic.png", mime, size, width, height, digest, time.Now().UTC()})
	}
	if _, err := f.db.CopyFrom(ctx, pgx.Identifier{"media", "assets"}, strings.Split(assetColumns, ","), pgx.CopyFromRows(rows)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "ANALYZE media.assets"); err != nil {
		t.Fatal(err)
	}
	page, err := f.service.List(ctx, testActor, "", 40)
	if err != nil || len(page.Assets) != 40 || page.NextCursor == "" {
		t.Fatal("real bounded page", err)
	}
	for _, query := range []struct {
		name, sql, index string
		args             []any
	}{
		{"list", "SELECT " + assetColumns + " FROM media.assets WHERE state='ready' AND id>$1 ORDER BY id LIMIT $2", "assets_ready_list", []any{page.Assets[39].ID, 41}},
		{"lookup", "SELECT " + assetColumns + " FROM media.assets WHERE id=$1", "assets_pkey", []any{page.Assets[0].ID}},
		{"reconcile", "SELECT id FROM media.assets WHERE state IN ('pending','deleting') ORDER BY id LIMIT $1", "assets_unfinished", []any{40}},
	} {
		var raw []byte
		if err := f.db.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query.sql, query.args...).Scan(&raw); err != nil {
			t.Fatal(query.name, err)
		}
		var plan any
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"Index Name": "`+query.index+`"`) {
			t.Fatal("expected real index not selected", query.name, string(raw))
		}
		t.Logf("%s actual EXPLAIN ANALYZE BUFFERS: %s", query.name, raw)
	}
}

func TestDurableCommitPolicyOverridesAsyncDSN(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	dsn := f.dsn + " synchronous_commit=off"
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal("durable migration with async DSN", err)
	}
	m, err := NewPostgres(dsn, Config{StorageRoot: f.root}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Start(ctx); err != nil {
		t.Fatal("durable module start", err)
	}
	defer func() {
		if err := m.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	var commit string
	if err = m.pool.QueryRow(ctx, "SHOW synchronous_commit").Scan(&commit); err != nil || commit != "on" {
		t.Fatal("intent may acknowledge before WAL durability", commit, err)
	}
}

func TestUnknownCleanupAndDeleteAcknowledgementsRetainState(t *testing.T) {
	f := newFixtureConfig(t, Config{MaxConns: 6, MaxOperations: 8})
	ctx, cancel := operationContext(t)
	defer cancel()
	ready, err := f.service.Create(ctx, testActor, "retained.png", bytes.NewReader(imageBytes(t, "png", 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(ctx, "CREATE FUNCTION media.lose_delete_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='deleting' THEN PERFORM pg_terminate_backend(pg_backend_pid()); END IF; RETURN NEW; END $$; CREATE TRIGGER lose_delete_ack BEFORE UPDATE ON media.assets FOR EACH ROW EXECUTE FUNCTION media.lose_delete_ack()"); err != nil {
		t.Fatal(err)
	}
	before := f.module.FailureCount()
	if err = f.service.Delete(ctx, testActor, ready.ID, ready.Revision); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatal("lost deleting acknowledgement not unknown", err)
	}
	if _, err = os.Stat(f.root + "/" + ready.ID); err != nil {
		t.Fatal("delete unlinked without durable intent acknowledgement", err)
	}
	a, err := f.service.Create(ctx, testActor, "bad.png", bytes.NewReader([]byte("malformed")))
	if !errors.Is(err, ErrInput) || !errors.Is(err, ErrUnknownOutcome) || !validID(a.ID) {
		t.Fatal("cleanup uncertainty lost", a, err)
	}
	if f.module.FailureCount() < before+2 {
		t.Fatal("unknown cleanup/delete omitted operational signals")
	}
	status, err := f.service.Status(ctx, testActor, a.ID)
	if err != nil || status.State != "pending" {
		t.Fatal("unknown cleanup invented state", status, err)
	}
	if _, err = f.db.Exec(ctx, "DROP TRIGGER lose_delete_ack ON media.assets"); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Reconcile(ctx, testActor, 40)
	if err != nil || result.Processed != 1 {
		t.Fatal("unknown cleanup reconciliation", result, err)
	}
	status, err = f.service.Status(ctx, testActor, ready.ID)
	if err != nil || status.State != "ready" {
		t.Fatal("reconcile changed retained ready asset", status, err)
	}
}

func TestPostgresConfiguredPoolAndIndependentValidationBudget(t *testing.T) {
	f := newFixtureConfig(t, Config{MaxConns: 6, MaxOperations: 8, AllowedMIMEs: []string{"image/png", "image/svg+xml"}})
	m := f.module
	if stat := m.pool.Stat(); stat.MaxConns() != 6 || stat.TotalConns() != 1 || cap(m.decoders) != 2 {
		t.Fatal("raised pool startup or fixed validation budget changed", stat)
	}
	ctx, cancel := operationContext(t)
	defer cancel()
	var release []func()
	defer func() {
		for _, done := range release {
			done()
		}
	}()
	for range 6 {
		lease, pool, _, finish, err := m.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := pool.Acquire(lease)
		if err != nil {
			finish()
			t.Fatal(err)
		}
		release = append(release, func() { conn.Release(); finish() })
	}
	if stat := m.pool.Stat(); stat.TotalConns() != 6 || stat.AcquiredConns() != 6 {
		t.Fatal("explicit Media pool above four was ineffective", stat)
	}
	wait, done := context.WithTimeout(ctx, 50*time.Millisecond)
	if err := m.Ready(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("native Media pool wait lost deadline", err)
	}
	done()
	if m.active != 6 {
		t.Fatal("pool wait leaked its owned lease")
	}
	for _, finish := range release {
		finish()
	}
	release = nil
	// Raising general admission cannot turn expensive work into eight slots.
	m.decoders <- struct{}{}
	m.decoders <- struct{}{}
	for _, input := range []struct {
		name string
		data []byte
	}{
		{"owned.svg", []byte(svgOriginal)}, {"owned.png", imageBytes(t, "png", 2, 2)},
	} {
		if _, err := f.service.Create(ctx, testActor, input.name, bytes.NewReader(input.data)); !errors.Is(err, ErrLimited) {
			t.Fatal("raised general admission expanded expensive validation", err)
		}
	}
	<-m.decoders
	<-m.decoders
	if err := m.Ready(ctx); err != nil || m.active != 0 || m.pool.Stat().TotalConns() != 6 {
		t.Fatal("configured pool or operation admission not reusable", err)
	}
}
