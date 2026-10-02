// SPDX-License-Identifier: MPL-2.0
package achrix_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
		"incompatible":         {&module{d: achrix.Descriptor{ID: "notes", Version: "1", Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 1}}}}},
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
		return achrix.ErrDenied
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, v := range []struct {
		p    achrix.Principal
		c, r string
	}{{"", "notes.read", "own"}, {"reader", "notes.read", "own"}, {"writer", "notes.read", "other"}, {"writer", "unknown", "own"}} {
		if err := a.Authorize(ctx, v.p, v.c, v.r); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
	}
	if err := a.Authorize(ctx, "writer", "notes.read", "own"); err != nil {
		t.Fatal(err)
	}
	b, err := achrix.New(config(), achrix.PolicyFunc(allow))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Authorize(ctx, "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrDenied) {
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

func TestReadAdmissionDuringLifecycle(t *testing.T) {
	startEntered, stopEntered := make(chan struct{}), make(chan struct{})
	startRelease, stopRelease := make(chan struct{}), make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(startRelease) })
	releaseStop := sync.OnceFunc(func() { close(stopRelease) })
	defer releaseStart()
	defer releaseStop()
	block := func(entered, release chan struct{}) func(context.Context) error {
		return func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	var readinessCalls, policyCalls atomic.Int32
	cfg := config()
	cfg.StartupTimeout, cfg.ShutdownTimeout = 5*time.Second, 5*time.Second
	a, err := achrix.New(cfg, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		policyCalls.Add(1)
		return nil
	}), &module{d: descriptor("notes"), start: block(startEntered, startRelease), stop: block(stopEntered, stopRelease), ready: func(context.Context) error {
		readinessCalls.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(ctx context.Context) error { return a.Authorize(ctx, "writer", "notes.read", "own") }
	operations := []struct {
		name string
		call func(context.Context) error
	}{{"Ready", a.Ready}, {"Authorize", authorize}}
	for _, phase := range []struct {
		name    string
		call    func(context.Context) error
		entered chan struct{}
		release func()
		calls   int32
	}{{"startup", a.Start, startEntered, releaseStart, 0}, {"shutdown", a.Shutdown, stopEntered, releaseStop, 1}} {
		t.Run(phase.name, func(t *testing.T) {
			defer phase.release()
			done := make(chan error, 1)
			go func() { done <- phase.call(context.Background()) }()
			select {
			case <-phase.entered:
			case <-time.After(time.Second):
				t.Fatal("lifecycle callback did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			canceled, cancelNow := context.WithCancel(ctx)
			cancelNow()
			type result struct {
				name      string
				err, want error
			}
			results := make(chan result, 4)
			for _, operation := range operations {
				for _, admission := range []struct {
					name string
					ctx  context.Context
					want error
				}{{"bounded", ctx, achrix.ErrNotReady}, {"canceled", canceled, context.Canceled}} {
					go func() {
						results <- result{operation.name + "/" + admission.name, operation.call(admission.ctx), admission.want}
					}()
				}
			}
			watchdog := time.NewTimer(250 * time.Millisecond)
			defer watchdog.Stop()
		admission:
			for remaining := 4; remaining > 0; remaining-- {
				select {
				case got := <-results:
					// A bounded call may start after its deadline if scheduling is delayed.
					boundedExpired := got.want == achrix.ErrNotReady &&
						errors.Is(ctx.Err(), context.DeadlineExceeded) &&
						errors.Is(got.err, context.DeadlineExceeded)
					if !errors.Is(got.err, got.want) && !boundedExpired {
						t.Errorf("%s: got %v, want %v", got.name, got.err, got.want)
					}
				case <-watchdog.C:
					t.Errorf("%d read calls blocked behind lifecycle exclusion", remaining)
					break admission
				}
			}
			if readinessCalls.Load() != phase.calls || policyCalls.Load() != phase.calls {
				t.Error("readiness or policy callback ran during lifecycle exclusion")
			}
			phase.release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("lifecycle did not finish after release")
			}
		})
		if phase.name == "startup" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			for _, operation := range operations {
				if err := operation.call(ctx); err != nil {
					t.Fatalf("%s after startup: %v", operation.name, err)
				}
			}
			cancel()
			if readinessCalls.Load() != 1 || policyCalls.Load() != 1 {
				t.Fatal("ordinary readiness or policy callback did not run")
			}
		}
	}
}

func TestOptionalCompositionAndABI(t *testing.T) {
	capability := achrix.Capability{ID: "provider.read", Version: 1}
	consumer := descriptor("consumer")
	consumer.Optional = []achrix.Capability{capability}
	provider := achrix.Descriptor{ID: "provider", Version: "1.9.0", Provides: []achrix.Capability{capability}}
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint("present=", present), func(t *testing.T) {
			mods := []achrix.Module{&module{d: consumer}}
			if present {
				mods = append(mods, &module{d: provider})
			}
			a, err := achrix.New(config(), achrix.PolicyFunc(allow), mods...)
			if err != nil {
				t.Fatal(err)
			}
			components := a.Components()
			if present && components[0].ID != "provider" {
				t.Fatal("present optional provider must start first", components)
			}
			last := len(components) - 1
			if !reflect.DeepEqual(components[last].Optional, consumer.Optional) {
				t.Fatal("optional metadata missing")
			}
			components[last].Optional[0].Version = 99
			if a.Components()[last].Optional[0].Version != 1 {
				t.Fatal("optional snapshot aliased")
			}
			if err := a.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, optional := range map[string][]achrix.Capability{
		"invalid identity": {{ID: "INVALID", Version: 1}},
		"zero revision":    {{ID: capability.ID}},
		"duplicate":        {capability, capability},
		"incompatible":     {{ID: capability.ID, Version: 2}},
	} {
		t.Run(name, func(t *testing.T) {
			d := consumer
			d.Optional = optional
			if _, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: d}, &module{d: provider}); !errors.Is(err, achrix.ErrComposition) {
				t.Fatal(err)
			}
		})
	}
	t.Run("required optional overlap", func(t *testing.T) {
		d := consumer
		d.Requires = []achrix.Capability{capability}
		if _, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: d}, &module{d: provider}); !errors.Is(err, achrix.ErrComposition) {
			t.Fatal(err)
		}
	})
	t.Run("present optional cycle", func(t *testing.T) {
		d := provider
		d.Optional = consumer.Provides
		if _, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: consumer}, &module{d: d}); !errors.Is(err, achrix.ErrComposition) {
			t.Fatal(err)
		}
	})
	t.Run("single ABI per identity", func(t *testing.T) {
		d := provider
		d.Provides = []achrix.Capability{capability, {ID: capability.ID, Version: 2}}
		if _, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: d}); !errors.Is(err, achrix.ErrComposition) {
			t.Fatal(err)
		}
	})
	t.Run("compatible implementation update", func(t *testing.T) {
		d := consumer
		d.Optional = nil
		d.Requires = []achrix.Capability{capability, {ID: "achrix.authorization", Version: 2}}
		for _, version := range []string{"1.0.0", "1.9.0", "2.0.0"} {
			p := provider
			p.Version = version // Implementation release does not change capability ABI.
			a, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: d}, &module{d: p})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestAuthorizationResultSemantics(t *testing.T) {
	private := errors.New("PRIVATE_POLICY_DEPENDENCY")
	for name, tc := range map[string]struct{ result, want error }{
		"allow":                   {},
		"explicit denial":         {achrix.ErrDenied, achrix.ErrDenied},
		"wrapped denial":          {fmt.Errorf("%w: %v", achrix.ErrDenied, private), achrix.ErrDenied},
		"dependency unavailable":  {private, achrix.ErrAuthorizationUnavailable},
		"dependency cancellation": {context.Canceled, achrix.ErrAuthorizationUnavailable},
		"dependency deadline":     {context.DeadlineExceeded, achrix.ErrAuthorizationUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			var logs strings.Builder
			cfg := config()
			cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			a, err := achrix.New(cfg, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return tc.result }), &module{d: descriptor("notes")})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer a.Shutdown(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = a.Authorize(ctx, "PRIVATE_PRINCIPAL", "notes.read", "PRIVATE_RESOURCE")
			if !errors.Is(err, tc.want) || errors.Is(err, private) || strings.Contains(fmt.Sprint(err), private.Error()) {
				t.Fatal("incorrect or unsafe public error", err)
			}
			for _, value := range []string{private.Error(), "PRIVATE_PRINCIPAL", "PRIVATE_RESOURCE"} {
				if strings.Contains(logs.String(), value) {
					t.Fatal("private policy data logged")
				}
			}
		})
	}
	for _, during := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("during=%v/deadline=%v", during, deadline), func(t *testing.T) {
				var cancel context.CancelFunc
				want := error(context.DeadlineExceeded)
				if !deadline {
					want = context.Canceled
				}
				var calls int
				a, err := achrix.New(config(), achrix.PolicyFunc(func(c context.Context, _ achrix.Principal, _, _ string) error {
					calls++
					if !deadline {
						cancel()
					}
					<-c.Done()
					return achrix.ErrDenied // Caller context outcome takes precedence.
				}), &module{d: descriptor("notes")})
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer a.Shutdown(context.Background())
				ctx, cancelNow := context.WithTimeout(context.Background(), 100*time.Millisecond)
				cancel = cancelNow
				defer cancel()
				if !during {
					if !deadline {
						cancel()
					}
					<-ctx.Done()
				}
				if err := a.Authorize(ctx, "writer", "notes.read", "own"); !errors.Is(err, want) {
					t.Fatal(err)
				}
				if (!during && calls != 0) || (during && calls != 1) {
					t.Fatal("incorrect admission", calls)
				}
			})
		}
	}
}

func TestAuthorizationRequiresDeadline(t *testing.T) {
	var calls atomic.Int32
	a, err := achrix.New(config(), achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { calls.Add(1); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	if err := a.Authorize(context.Background(), "writer", "achrix.authorization", ""); !errors.Is(err, achrix.ErrAuthorizationUnavailable) || calls.Load() != 0 {
		t.Fatal("unbounded policy admitted", err, calls.Load())
	}
}

func TestShutdownCancelsAndDrainsCoreCallbacks(t *testing.T) {
	for _, readiness := range []bool{false, true} {
		t.Run(fmt.Sprint("readiness=", readiness), func(t *testing.T) {
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseCallback := sync.OnceFunc(func() { close(release) })
			defer releaseCallback()
			var active atomic.Bool
			var stops atomic.Int32
			block := func(ctx context.Context) error {
				active.Store(true)
				defer active.Store(false)
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release // Controlled, brief cancellation cleanup before returning.
				return ctx.Err()
			}
			m := &module{d: descriptor("notes"), stop: func(context.Context) error {
				if active.Load() {
					t.Error("Stop raced a Core callback")
				}
				stops.Add(1)
				return nil
			}}
			policy := achrix.PolicyFunc(allow)
			if readiness {
				m.ready = block
			} else {
				policy = func(c context.Context, _ achrix.Principal, _, _ string) error { return block(c) }
			}
			a, err := achrix.New(config(), policy, m)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if readiness {
					done <- a.Ready(ctx)
				} else {
					done <- a.Authorize(ctx, "writer", "notes.read", "own")
				}
			}()
			awaitSignal(t, entered)
			shutdown := make(chan error, 2)
			go func() { shutdown <- a.Shutdown(context.Background()) }()
			awaitSignal(t, canceled)
			go func() { shutdown <- a.Shutdown(context.Background()) }()
			if stops.Load() != 0 {
				t.Fatal("stopped before drain")
			}
			if err := a.Authorize(ctx, "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrNotReady) {
				t.Fatal(err)
			}
			if err := a.Ready(ctx); !errors.Is(err, achrix.ErrNotReady) {
				t.Fatal(err)
			}
			releaseCallback()
			if err := awaitResult(t, done); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			for range 2 {
				if err := awaitResult(t, shutdown); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Shutdown(context.Background()); err != nil || stops.Load() != 1 {
				t.Fatal(err, stops.Load())
			}
		})
	}
}

func TestShutdownDrainTimeoutRemainsClosedAndCanRetry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseCallback := sync.OnceFunc(func() { close(release) })
	defer releaseCallback()
	var stops atomic.Int32
	cfg := config()
	cfg.ShutdownTimeout = 20 * time.Millisecond
	a, err := achrix.New(cfg, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		close(entered)
		<-release // Deliberately noncompliant until released: Core cannot sandbox it.
		return nil
	}), &module{d: descriptor("notes"), stop: func(context.Context) error { stops.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Authorize(ctx, "writer", "notes.read", "own") }()
	awaitSignal(t, entered)
	shutdown := make(chan error, 1)
	go func() { shutdown <- a.Shutdown(context.Background()) }()
	if err := awaitResult(t, shutdown); !errors.Is(err, context.DeadlineExceeded) || stops.Load() != 0 {
		t.Fatal(err, stops.Load())
	}
	if err := a.Authorize(ctx, "writer", "notes.read", "own"); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal(err)
	}
	releaseCallback()
	if err := awaitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil || stops.Load() != 1 {
		t.Fatal(err, stops.Load())
	}
}

func TestShutdownDuringStartup(t *testing.T) {
	entered := make(chan struct{})
	var starting atomic.Bool
	var stops atomic.Int32
	a, err := achrix.New(config(), achrix.PolicyFunc(allow), &module{d: descriptor("notes"), start: func(ctx context.Context) error {
		starting.Store(true)
		defer starting.Store(false)
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, stop: func(ctx context.Context) error {
		if starting.Load() || ctx.Err() != nil {
			t.Error("unsafe partial-start cleanup")
		}
		stops.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Start(context.Background()) }()
	awaitSignal(t, entered)
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := awaitResult(t, done); !errors.Is(err, context.Canceled) || stops.Load() != 1 {
		t.Fatal(err, stops.Load())
	}
	if err := a.Start(context.Background()); !errors.Is(err, achrix.ErrNotReady) {
		t.Fatal(err)
	}
}

func TestShutdownStartupWaitHasDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseStart := sync.OnceFunc(func() { close(release) })
	defer releaseStart()
	var stops atomic.Int32
	cfg := config()
	cfg.ShutdownTimeout = 20 * time.Millisecond
	a, err := achrix.New(cfg, achrix.PolicyFunc(allow), &module{d: descriptor("notes"), start: func(context.Context) error {
		close(entered)
		<-release // Controlled noncompliance proves bounded lifecycle waiting.
		return nil
	}, stop: func(context.Context) error { stops.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() { started <- a.Start(context.Background()) }()
	awaitSignal(t, entered)
	shutdown := make(chan error, 1)
	go func() { shutdown <- a.Shutdown(context.Background()) }()
	if err := awaitResult(t, shutdown); !errors.Is(err, context.DeadlineExceeded) || stops.Load() != 0 {
		t.Fatal(err, stops.Load())
	}
	releaseStart()
	if err := awaitResult(t, started); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil || stops.Load() != 1 {
		t.Fatal(err, stops.Load())
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("callback did not reach expected phase")
	}
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("bounded operation did not return")
		return nil
	}
}
