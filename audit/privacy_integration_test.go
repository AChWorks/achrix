// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAuditDependencyErrorsAreCanonical(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cause      error
		want       error
		diagnostic bool
	}{
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded, false},
		{"canceled", context.Canceled, context.Canceled, false},
		{"provider", errors.New("provider unavailable"), ErrUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := testDSN(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
				defer closeCancel()
				_ = conn.Close(closeCtx)
			})
			if _, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
				t.Fatal(err)
			}
			if err = Migrate(ctx, dsn); err != nil {
				t.Fatal(err)
			}

			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			m, err := NewPostgres(dsn, Config{}, logger)
			if err != nil {
				t.Fatal(err)
			}
			policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
				if actor == "reader" && capability == Query && target == "ACCOUNT" {
					return nil
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
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer stopCancel()
				if err := app.Shutdown(stopCtx); err != nil {
					t.Error(err)
				}
			})
			service, err := NewService(app, m)
			if err != nil {
				t.Fatal(err)
			}

			config := m.pool.Config()
			const private = "private-audit-connection password=hidden"
			config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
				return nil, fmt.Errorf("%s: %w", private, tc.cause)
			}
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}

			connection, dependencyErr := pool.Acquire(ctx)
			if connection != nil {
				connection.Release()
			}
			var wrapped *pgconn.ConnectError
			if !errors.Is(dependencyErr, tc.cause) || !errors.As(dependencyErr, &wrapped) || !strings.Contains(dependencyErr.Error(), private) || ctx.Err() != nil {
				pool.Close()
				t.Fatal("driver did not produce the expected private wrapped dependency error")
			}

			before := m.FailureCount()
			m.pool.Close()
			m.mu.Lock()
			m.pool = pool
			m.mu.Unlock()

			page, got := service.Query(ctx, "reader", "ACCOUNT", "", 1)
			var escaped *pgconn.ConnectError
			if got != tc.want || errors.As(got, &escaped) || strings.Contains(got.Error(), private) || ctx.Err() != nil || len(page.Records) != 0 || page.NextCursor != "" {
				t.Fatalf("Audit dependency failure was not canonical with a live caller: %v", got)
			}
			wantFailures := before
			if tc.diagnostic {
				wantFailures++
			}
			if m.FailureCount() != wantFailures || strings.Contains(logs.String(), private) {
				t.Fatal("dependency failure leaked or changed bounded diagnostics")
			}
			m.mu.Lock()
			active := m.active
			m.mu.Unlock()
			if active != 0 || pool.Stat().AcquiredConns() != 0 {
				t.Fatal("dependency failure leaked Audit ownership")
			}
		})
	}
}
