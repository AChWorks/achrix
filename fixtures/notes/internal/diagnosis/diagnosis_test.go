// SPDX-License-Identifier: MPL-2.0
package diagnosis

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeBudgetCorrelationAndCardinality(t *testing.T) {
	var output strings.Builder
	now := time.Unix(100, 0)
	b := &budget{now: func() time.Time { return now }}
	logger := slog.New(handler{inner: slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}), budget: b})
	ctx, id := Correlate(context.Background())
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		for range 10000 {
			// Every request gets different trusted correlation; the budget still
			// retains exactly four windows and no request/principal/IP map.
			request, _ := Correlate(ctx)
			logger.Log(request, level, "failure", "component", "notes.http", "operation", "request", "reason", "permission_denied")
		}
	}
	if got := strings.Count(output.String(), "\n"); got != 4 {
		t.Fatalf("emitted %d records in a single window", got)
	}
	// Lifecycle failures remain visible while request traffic consumes every budget.
	logger.Error("startup failed", "component", "notes.consumer", "operation", "start", "reason", "module_failed")
	if got := strings.Count(output.String(), "\n"); got != 5 {
		t.Fatalf("lifecycle diagnostic was suppressed: %d", got)
	}
	now = now.Add(time.Second)
	logger.InfoContext(ctx, "request failed", "component", "notes.http", "operation", "request", "reason", "unauthenticated")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
		t.Fatal(err)
	}
	if record["request_id"] != id || record["suppressed_records"] != float64(9999) || record["reason"] != "unauthenticated" {
		t.Fatal("missing current correlation/category or aggregate count", record)
	}
}

func TestBudgetSharedAcrossDerivedLoggersAndConcurrentRequests(t *testing.T) {
	var output strings.Builder // Only one concurrent call reaches the JSON handler.
	b := &budget{now: func() time.Time { return time.Unix(100, 0) }}
	logger := slog.New(handler{inner: slog.NewJSONHandler(&output, nil), budget: b})
	derived := logger.With("component", "notes.postgresql", "operation", "readiness").WithGroup("detail")
	var workers sync.WaitGroup
	for range 1000 {
		workers.Go(func() {
			derived.Warn("dependency degraded", "reason", "connection_failure")
			ctx, _ := Correlate(context.Background())
			logger.WarnContext(ctx, "request failed", "reason", "unavailable")
		})
	}
	workers.Wait()
	if got := strings.Count(output.String(), "\n"); got != 1 || b.windows[2].suppressed != 1999 {
		t.Fatalf("derived/concurrent loggers escaped shared budget: records=%d suppressed=%d", got, b.windows[2].suppressed)
	}
	b.windows[2].suppressed = ^uint64(0)
	logger.WarnContext(context.WithValue(context.Background(), requestKey{}, "trusted"), "failure")
	if b.windows[2].suppressed != ^uint64(0) {
		t.Fatal("suppression count wrapped")
	}
}

func TestDirectRuntimeOperationsAndFilteredDebug(t *testing.T) {
	var output strings.Builder
	logger := New(&output)
	logger.DebugContext(context.Background(), "authorization denied", "operation", "authorize", "reason", "policy_denied")
	if output.Len() != 0 {
		t.Fatal("debug enabled by default")
	}
	for range 1000 {
		logger.Warn("readiness degraded", "component", "notes.postgresql", "operation", "readiness", "reason", "connection_failure")
	}
	if got := strings.Count(output.String(), "\n"); got != 1 {
		t.Fatalf("direct readiness emitted %d records", got)
	}
}
