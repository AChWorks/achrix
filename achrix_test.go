// SPDX-License-Identifier: MPL-2.0
package achrix_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

type module struct {
	d           achrix.Descriptor
	start, stop func(context.Context) error
	ready       func(context.Context) error
}

func (m *module) Descriptor() achrix.Descriptor { return m.d }
func (m *module) Start(c context.Context) error {
	if m.start != nil {
		return m.start(c)
	}
	return nil
}
func (m *module) Stop(c context.Context) error {
	if m.stop != nil {
		return m.stop(c)
	}
	return nil
}
func (m *module) Ready(c context.Context) error {
	if m.ready != nil {
		return m.ready(c)
	}
	return nil
}
func descriptor(id string) achrix.Descriptor {
	return achrix.Descriptor{ID: id, Version: "0.1.0", Provides: []achrix.Capability{{ID: id + ".read", Version: 1}}}
}
func config() achrix.Config {
	return achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func allow(context.Context, achrix.Principal, string, string) error { return nil }

func TestCompositionRejectsBeforeEffects(t *testing.T) {
	base := descriptor("notes")
	tests := map[string][]achrix.Module{
		"nil":                  {nil},
		"duplicate module":     {&module{d: base}, &module{d: base}},
		"invalid identity":     {&module{d: descriptor("NOTES")}},
		"duplicate capability": {&module{d: base}, &module{d: achrix.Descriptor{ID: "other", Version: "1", Provides: base.Provides}}},
		"missing":              {&module{d: achrix.Descriptor{ID: "notes", Version: "1", Requires: []achrix.Capability{{ID: "missing", Version: 1}}}}},
		"incompatible":         {&module{d: achrix.Descriptor{ID: "notes", Version: "1", Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}}},
		"cycle":                {&module{d: achrix.Descriptor{ID: "one", Version: "1", Provides: []achrix.Capability{{ID: "one", Version: 1}}, Requires: []achrix.Capability{{ID: "two", Version: 1}}}}, &module{d: achrix.Descriptor{ID: "two", Version: "1", Provides: []achrix.Capability{{ID: "two", Version: 1}}, Requires: []achrix.Capability{{ID: "one", Version: 1}}}}},
	}
	for name, mods := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := achrix.New(config(), achrix.PolicyFunc(allow), mods...); !errors.Is(err, achrix.ErrComposition) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := achrix.New(config(), nil); !errors.Is(err, achrix.ErrComposition) {
		t.Fatal(err)
	}
	bad := config()
	bad.StartupTimeout = 0
	if _, err := achrix.New(bad, achrix.PolicyFunc(allow)); !errors.Is(err, achrix.ErrComposition) {
		t.Fatal(err)
	}
}

func TestOrderedStartupPartialFailureCleanupAndSnapshot(t *testing.T) {
	var events []string
	first := &module{d: descriptor("storage"), start: func(context.Context) error { events = append(events, "start storage"); return nil }, stop: func(c context.Context) error {
		if c.Err() != nil {
			t.Fatal("cleanup inherited cancellation")
		}
		events = append(events, "stop storage")
		return nil
	}}
	second := &module{d: descriptor("notes"), start: func(c context.Context) error {
		events = append(events, "start notes")
		return errors.New("credential=SECRET")
	}, stop: func(context.Context) error { events = append(events, "stop notes"); return nil }}
	second.d.Requires = []achrix.Capability{{ID: "storage.read", Version: 1}}
	var logs strings.Builder
	cfg := config()
	cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	a, err := achrix.New(cfg, achrix.PolicyFunc(allow), second, first)
	if err != nil {
		t.Fatal(err)
	}
	first.d.Provides[0].ID = "mutated"
	if a.Components()[0].Provides[0].ID != "storage.read" {
		t.Fatal("descriptor aliased")
	}
	snapshot := a.Components()
	snapshot[0].Provides[0].ID = "mutated again"
	if a.Components()[0].Provides[0].ID != "storage.read" {
		t.Fatal("metadata aliased")
	}
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	if !reflect.DeepEqual(events, []string{"start storage", "start notes", "stop notes", "stop storage"}) {
		t.Fatal(events)
	}
	if strings.Contains(logs.String(), "SECRET") || !strings.Contains(logs.String(), `"component":"notes"`) {
		t.Fatal(logs.String())
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatal("cleanup repeated")
	}
}

func TestAuthorizationAndInstanceIsolation(t *testing.T) {
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, c, r string) error {
		if p == "writer" && c == "notes.read" && r == "own" {
			return nil
		}
		return errors.New("deny")
	})
	a, err := achrix.New(config(), policy, &module{d: descriptor("notes")})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Authorize(context.Background(), "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		p    achrix.Principal
		c, r string
	}{{"", "notes.read", "own"}, {"reader", "notes.read", "own"}, {"writer", "notes.read", "other"}, {"writer", "unknown", "own"}} {
		if err := a.Authorize(context.Background(), v.p, v.c, v.r); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
	}
	if err := a.Authorize(context.Background(), "writer", "notes.read", "own"); err != nil {
		t.Fatal(err)
	}
	b, err := achrix.New(config(), achrix.PolicyFunc(allow))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Authorize(context.Background(), "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("registry leaked")
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Authorize(context.Background(), "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal(err)
	}
}

func TestLifecycleDeadlinesAndReadiness(t *testing.T) {
	cfg := config()
	cfg.StartupTimeout = 20 * time.Millisecond
	cleaned := false
	a, err := achrix.New(cfg, achrix.PolicyFunc(allow), &module{d: descriptor("storage"), start: func(c context.Context) error { <-c.Done(); return c.Err() }, stop: func(c context.Context) error { cleaned = true; return c.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); !errors.Is(err, context.DeadlineExceeded) || !cleaned {
		t.Fatal(err, cleaned)
	}
	b, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: descriptor("notes"), ready: func(c context.Context) error { return errors.New("unavailable") }})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.Ready(ctx); err == nil {
		t.Fatal("readiness passed failed dependency")
	}
	if err := b.Ready(context.Background()); err == nil {
		t.Fatal("unbounded readiness")
	}
	cfg = config()
	cfg.ShutdownTimeout = 20 * time.Millisecond
	c, err := achrix.New(cfg, achrix.PolicyFunc(allow), &module{d: descriptor("notes"), stop: func(c context.Context) error { <-c.Done(); return c.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
