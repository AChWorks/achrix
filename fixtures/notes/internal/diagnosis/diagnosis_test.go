// SPDX-License-Identifier: MPL-2.0
package diagnosis

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestDistinctFailuresCorrelateAndFlushWithoutFurtherTraffic(t *testing.T) {
	var output lockedOutput
	logger := New(&output)
	defer Flush(logger)
	ctx, id := Correlate(context.Background())
	again, same := Correlate(ctx)
	if same != id || again != ctx {
		t.Fatal("nested boundary replaced trusted correlation")
	}
	logger.ErrorContext(ctx, "failed", "component", "db", "operation", "insert", "phase", "execute", "reason", "permission", "outcome", "not_executed")
	logger.ErrorContext(ctx, "failed", "component", "db", "operation", "insert", "phase", "commit", "reason", "connection_lost", "outcome", "unknown")
	for range 1000 {
		logger.ErrorContext(ctx, "failed", "component", "db", "operation", "insert", "phase", "execute", "reason", "permission", "outcome", "not_executed")
	}
	if got := strings.Count(output.String(), "\n"); got != 2 {
		t.Fatalf("distinct causes lost or repetition unbounded: %d", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(output.String(), `"suppressed_records":1000`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], id) || !strings.Contains(lines[1], `"outcome":"unknown"`) {
		t.Fatal(output.String())
	}
	if !strings.Contains(lines[2], `"diagnostic_summary":true`) || strings.Contains(lines[2], "request_id") {
		t.Fatal("summary lost or borrowed request identity", lines[2])
	}
}

func TestBudgetFixedCapacityAndUnknownOutcomeReservation(t *testing.T) {
	var output lockedOutput
	inner := slog.NewJSONHandler(&output, nil)
	b := &budget{now: time.Now, inner: inner}
	logger := slog.New(handler{inner: inner, budget: b})
	ctx, _ := Correlate(context.Background())
	for i := range 1000 {
		logger.ErrorContext(ctx, "failure", "operation", "readiness", "reason", fmt.Sprintf("reason_%d", i))
	}
	logger.ErrorContext(ctx, "failure", "operation", "insert", "phase", "commit", "reason", "connection_lost", "outcome", "unknown")
	if got := strings.Count(output.String(), "\n"); got != 33 {
		t.Fatalf("capacity changed or unknown hidden: %d", got)
	}
	Flush(logger)
	if !strings.Contains(output.String(), `"distinct_capacity_exceeded":true`) || !strings.Contains(output.String(), `"suppressed_records":968`) {
		t.Fatal("overflow evidence missing", output.String())
	}
}

func TestBudgetSharedAcrossDerivedLoggersAndConcurrentRequests(t *testing.T) {
	var output lockedOutput
	inner := slog.NewJSONHandler(&output, nil)
	b := &budget{now: time.Now, inner: inner}
	logger := slog.New(handler{inner: inner, budget: b})
	derived := logger.With("component", "db", "operation", "readiness").WithGroup("detail")
	var workers sync.WaitGroup
	for range 1000 {
		workers.Go(func() { derived.Warn("failure", "reason", "connection_failure") })
	}
	workers.Wait()
	Flush(logger)
	if got := strings.Count(output.String(), "\n"); got != 2 {
		t.Fatalf("derived concurrency budget: %d", got)
	}
	if !strings.Contains(output.String(), `"suppressed_records":999`) {
		t.Fatal(output.String())
	}
}

func TestComponentFinalSummaryBypassesProductBudget(t *testing.T) {
	var output lockedOutput
	logger := New(&output)
	defer Flush(logger)
	ctx, _ := Correlate(context.Background())
	for range 20 {
		logger.ErrorContext(ctx, "failed", "operation", "insert", "reason", "io")
	}
	logger.ErrorContext(context.Background(), "component summary", "operation", "insert", "reason", "io", "diagnostic_summary", true, "suppressed_records", 50)
	if !strings.Contains(output.String(), `"suppressed_records":50`) {
		t.Fatal("only final component evidence was discarded")
	}
	logger.Debug("denied", "operation", "authorize", "reason", "policy_denied")
	if strings.Contains(output.String(), "policy_denied") {
		t.Fatal("debug enabled by default")
	}
}

func TestLateTimerCannotEraseReplacementWindow(t *testing.T) {
	var output lockedOutput
	inner := slog.NewJSONHandler(&output, nil)
	now := time.Now()
	b := &budget{now: func() time.Time { return now }, inner: inner}
	fields := diagnosticFields{"db", "insert", "commit", "connection_lost", "unknown", "provider"}
	b.admit(slog.LevelError, fields)
	stale := b.generation
	now = now.Add(2 * time.Second)
	b.admit(slog.LevelError, fields)
	b.admit(slog.LevelError, fields)
	b.flushGeneration(stale)
	if output.String() != "" {
		t.Fatal("late timer flushed replacement window")
	}
	b.flush()
	if !strings.Contains(output.String(), `"suppressed_records":1`) {
		t.Fatal("replacement evidence lost", output.String())
	}
}
