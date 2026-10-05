// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresIdentityDependencyErrorsAreCanonical(t *testing.T) {
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
			f := newIdentityFixture(t)
			ctx, cancel := deadline()
			defer cancel()

			config := f.module.pool.Config()
			const private = "private-identity-connection password=hidden"
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

			before := f.module.FailureCount()
			f.module.pool.Close()
			f.module.mu.Lock()
			f.module.pool = pool
			f.module.mu.Unlock()

			_, got := f.service.Login(context.Background(), "fixture", testPassword, "")
			var escaped *pgconn.ConnectError
			if got != tc.want || errors.As(got, &escaped) || strings.Contains(got.Error(), private) || ctx.Err() != nil {
				t.Fatalf("Identity dependency failure was not canonical with a live caller: %v", got)
			}
			wantFailures := before
			if tc.diagnostic {
				wantFailures++
			}
			if f.module.FailureCount() != wantFailures || strings.Contains(f.logs.String(), private) {
				t.Fatal("dependency failure leaked or changed bounded diagnostics")
			}
			f.module.mu.Lock()
			active := f.module.active
			f.module.mu.Unlock()
			if active != 0 || pool.Stat().AcquiredConns() != 0 {
				t.Fatal("dependency failure leaked Identity ownership")
			}
		})
	}
}
