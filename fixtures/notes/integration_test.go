// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/infrastructure"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The suite fails, rather than skipping, when its real disposable DB is absent.
// validate.sh provisions it; these tests never accept a production database name.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NOTES_TEST_DATABASE_URL")
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || !strings.HasPrefix(c.ConnConfig.Database, "achrix_test_") {
		t.Fatal("isolated NOTES_TEST_DATABASE_URL required")
	}
	c.MaxConns = 4
	c.ConnConfig.ConnectTimeout = time.Second
	p, err := pgxpool.NewWithConfig(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil || version < 180000 || version >= 190000 {
		t.Fatal("PostgreSQL 18 required", version, err)
	}
	return p
}
func reset(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(context.Background(), "DROP SCHEMA IF EXISTS notes CASCADE"); err != nil {
		t.Fatal(err)
	}
}
func migrate(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := infrastructure.Migrate(ctx, p, infrastructure.Migrations()); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMigrationAndSharedBehavior(t *testing.T) {
	p := testPool(t)
	reset(t, p)
	t.Run("real partial startup cleans the acquired pool", func(t *testing.T) {
		logger := diagnosis.New(io.Discard)
		s, err := infrastructure.New(os.Getenv("NOTES_TEST_DATABASE_URL"), logger)
		if err != nil {
			t.Fatal(err)
		}
		a, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), s)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Start(context.Background()); err == nil {
			t.Fatal("uninstalled schema started")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Ready(ctx); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatal("failed resource not cleaned", err)
		}
		if err := a.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("transaction failure and interruption are reenterable", func(t *testing.T) {
		for _, statement := range []string{"SELECT 1/0;", "SELECT pg_sleep(10);"} {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			m := infrastructure.Migrations()
			m[0].SQL += statement
			if err := infrastructure.Migrate(ctx, p, m); err == nil {
				t.Fatal("failed/interrupted migration passed")
			}
			cancel()
			var present bool
			if err := p.QueryRow(context.Background(), "SELECT to_regclass('notes.entries') IS NOT NULL OR to_regclass('notes.migrations') IS NOT NULL").Scan(&present); err != nil || present {
				t.Fatal("partial schema/ledger survived", present, err)
			}
		}
	})
	t.Run("concurrent migration and immutable identity", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				errs <- infrastructure.Migrate(ctx, p, infrastructure.Migrations())
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		migrate(t, p)
		var count int
		if err := p.QueryRow(context.Background(), "SELECT count(*) FROM notes.migrations").Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		changed := infrastructure.Migrations()
		changed[0].SQL += "\n-- changed immutable content"
		if err := infrastructure.Migrate(context.Background(), p, changed); err == nil {
			t.Fatal("changed migration accepted")
		}
		if err := infrastructure.CheckSchema(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		_, err := p.Exec(context.Background(), "INSERT INTO notes.migrations VALUES('999_unknown',repeat('0',64))")
		if err != nil {
			t.Fatal(err)
		}
		if infrastructure.Migrate(context.Background(), p, infrastructure.Migrations()) == nil || infrastructure.CheckSchema(context.Background(), p) == nil {
			t.Fatal("unknown schema accepted")
		}
		if _, err := p.Exec(context.Background(), "DELETE FROM notes.migrations WHERE id='999_unknown'"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Application and HTTP share persistence and policy", func(t *testing.T) {
		logger := diagnosis.New(io.Discard)
		s, err := infrastructure.New(os.Getenv("NOTES_TEST_DATABASE_URL"), logger)
		if err != nil {
			t.Fatal(err)
		}
		app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), s)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := app.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		})
		service := application.New(app, s, logger)
		ctx, _ := diagnosis.Correlate(context.Background())
		n, err := service.Create(ctx, "writer", "یادداشت پایدار از فراخوانی مستقیم")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Create(ctx, "reader", "denied"); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
		if _, err := service.Read(ctx, "outsider", n.ID); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
		if _, err := service.Create(ctx, "writer", strings.Repeat("x", 201)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
		writer, reader := rand.Text()+rand.Text(), rand.Text()+rand.Text()
		handler := presentation.New(app, service, writer, reader, logger)
		request := func(method, path, token, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		w := request("GET", "/notes/"+n.ID, reader, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var got domain.Note
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.ID != n.ID || got.Text != n.Text {
			t.Fatal(got, err)
		}
		w = request("POST", "/notes", writer, `{"text":"Created through HTTP"}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if found, err := service.Read(ctx, "reader", got.ID); err != nil || found.Text != got.Text {
			t.Fatal(found, err)
		}
		for _, v := range []struct {
			token, body string
			status      int
		}{{reader, `{"text":"denied"}`, 403}, {"", `{"text":"no authentication"}`, 401}, {writer, `{"text":"ok","principal":"writer"}`, 400}, {writer, `{"text":"ok"} {}`, 400}, {writer, `{"text":"` + strings.Repeat("x", 1100) + `"}`, 400}} {
			if w := request("POST", "/notes", v.token, v.body); w.Code != v.status {
				t.Fatal(w.Code, v.status)
			}
		}
		var count int
		if err := p.QueryRow(ctx, "SELECT count(*) FROM notes.entries").Scan(&count); err != nil || count != 2 {
			t.Fatal("unauthorized/invalid state change", count, err)
		}
		// Database constraints preserve durable invariants even outside Application.
		for _, text := range []string{"", strings.Repeat("ا", 201)} {
			if _, err := p.Exec(ctx, "INSERT INTO notes.entries(id,body,created_at) VALUES($1,$2,now())", rand.Text(), text); err == nil {
				t.Fatal("SQL constraint missing")
			}
		}
		w = request("GET", "/ready", "", "")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		if err := app.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if w := request("GET", "/ready", "", ""); w.Code != 503 {
			t.Fatal("stopped app ready")
		}
		// A fresh composition demonstrates persistence across instance lifecycle.
		s2, err := infrastructure.New(os.Getenv("NOTES_TEST_DATABASE_URL"), logger)
		if err != nil {
			t.Fatal(err)
		}
		a2, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), s2)
		if err != nil {
			t.Fatal(err)
		}
		if err := a2.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer a2.Shutdown(context.Background())
		if found, err := application.New(a2, s2, logger).Read(ctx, "reader", n.ID); err != nil || found.Text != n.Text {
			t.Fatal(found, err)
		}
	})
	t.Run("safe correlated failure attribution", func(t *testing.T) {
		var output safeBuffer
		logger := diagnosis.New(&output)
		s, err := infrastructure.New(os.Getenv("NOTES_TEST_DATABASE_URL"), logger)
		if err != nil {
			t.Fatal(err)
		}
		a, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), s)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer a.Shutdown(context.Background())
		service := application.New(a, s, logger)
		token := rand.Text() + rand.Text()
		h := presentation.New(a, service, token, rand.Text()+rand.Text(), logger)
		// Controlled transaction-safe fault in this disposable test schema.
		if _, err := p.Exec(context.Background(), "ALTER TABLE notes.entries RENAME TO entries_unavailable"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := p.Exec(context.Background(), "ALTER TABLE notes.entries_unavailable RENAME TO entries"); err != nil {
				t.Error(err)
			}
		}()
		r := httptest.NewRequest("POST", "/notes", strings.NewReader(`{"text":"PRIVATE_FIXTURE_BODY"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "unavailable") {
			t.Fatal(w.Code, w.Body.String())
		}
		id := w.Header().Get("X-Request-ID")
		if id == "" {
			t.Fatal("no correlation")
		}
		logs := output.String()
		for _, component := range []string{"notes.postgresql", "notes.application", "notes.http"} {
			found := false
			for _, line := range strings.Split(logs, "\n") {
				if strings.Contains(line, component) && strings.Contains(line, id) {
					found = true
				}
			}
			if !found {
				t.Fatal("missing correlated component", component, logs)
			}
		}
		// Exercise the same Core policy denial with a correlated HTTP call.
		reader := rand.Text() + rand.Text()
		h = presentation.New(a, service, token, reader, logger)
		r = httptest.NewRequest("POST", "/notes", strings.NewReader(`{"text":"denied"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+reader)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(output.String(), "achrix.authorization") {
			t.Fatal("Core attribution missing")
		}
		for _, secret := range []string{token, reader, "PRIVATE_FIXTURE_BODY", os.Getenv("NOTES_TEST_DATABASE_URL"), "INSERT INTO"} {
			if strings.Contains(output.String(), secret) || strings.Contains(w.Body.String(), secret) {
				t.Fatal("sensitive diagnostic leak")
			}
		}
	})
}

type safeBuffer struct {
	mu   sync.Mutex
	data strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *safeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

func TestTrustedRestore(t *testing.T) {
	if os.Getenv("NOTES_RESTORE_VERIFY") != "1" {
		t.Skip("validate.sh runs this explicitly after its trusted isolated restore")
	}
	p := testPool(t)
	if err := infrastructure.CheckSchema(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(context.Background(), "SELECT count(*) FROM notes.entries").Scan(&count); err != nil || count != 2 {
		t.Fatal("restored data mismatch", count, err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s, err := infrastructure.New(os.Getenv("NOTES_TEST_DATABASE_URL"), logger)
	if err != nil {
		t.Fatal(err)
	}
	a, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), s)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var id, text string
	if err := p.QueryRow(ctx, "SELECT id,body FROM notes.entries ORDER BY id LIMIT 1").Scan(&id, &text); err != nil {
		t.Fatal(err)
	}
	if n, err := application.New(a, s, logger).Read(ctx, "reader", id); err != nil || n.Text != text {
		t.Fatal(fmt.Sprint(n), err)
	}
}
