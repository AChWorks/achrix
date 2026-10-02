// SPDX-License-Identifier: MPL-2.0
package achrix_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

// These callbacks represent cheap local checks; the measurements isolate Core
// admission/cancellation and traversal, not identity, database or product work.
type benchmarkModule struct {
	descriptor achrix.Descriptor
}

func (m benchmarkModule) Descriptor() achrix.Descriptor { return m.descriptor }
func (benchmarkModule) Start(ctx context.Context) error { return ctx.Err() }
func (benchmarkModule) Ready(ctx context.Context) error { return ctx.Err() }
func (benchmarkModule) Stop(ctx context.Context) error  { return ctx.Err() }

func benchmarkApplication(b *testing.B, modules ...achrix.Module) (*achrix.Application, context.Context) {
	b.Helper()
	app, err := achrix.New(achrix.Config{
		StartupTimeout: time.Second, ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _, _ string) error {
		return ctx.Err()
	}), modules...)
	if err != nil {
		b.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			b.Error(err)
		}
	})
	// Production callers own operation deadlines. Reuse a valid parent so this
	// benchmark measures the Application path rather than a caller's timer setup.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	b.Cleanup(cancel)
	return app, ctx
}

func BenchmarkApplicationAuthorizeAllowed(b *testing.B) {
	app, ctx := benchmarkApplication(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := app.Authorize(ctx, "benchmark-user", "achrix.authorization", "benchmark-resource"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplicationReadyThreeModules(b *testing.B) {
	authorization := achrix.Capability{ID: "achrix.authorization", Version: 2}
	identity := achrix.Capability{ID: "benchmark.identity", Version: 1}
	app, ctx := benchmarkApplication(b,
		benchmarkModule{achrix.Descriptor{ID: "benchmark.identity", Version: "1", Provides: []achrix.Capability{identity}}},
		benchmarkModule{achrix.Descriptor{ID: "benchmark.notes", Version: "1", Requires: []achrix.Capability{authorization, identity}}},
		benchmarkModule{achrix.Descriptor{ID: "benchmark.settings", Version: "1", Requires: []achrix.Capability{authorization}}},
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := app.Ready(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
