// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
)

func inventory() multisite.Config {
	return multisite.Config{Sites: []multisite.Site{
		{ID: "Alpha_1", Authorities: []string{"alpha.example", "alpha.example:443", "xn--bcher-kva.example", "127.0.0.1", "[2001:db8::1]", "[::ffff:192.0.2.1]:65535"}},
		{ID: "Beta-2", Authorities: []string{"beta.example", "alpha.example:8443", "127.0.0.1:1", "[2001:db8::1]:443"}},
		{ID: "Disabled", Authorities: []string{"disabled.example"}, Disabled: true},
	}}
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func appConfig() achrix.Config {
	return achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func compose(t *testing.T, config multisite.Config, policy achrix.Policy) (*achrix.Application, *multisite.Module, *multisite.Service) {
	t.Helper()
	m, err := multisite.New(config)
	if err != nil {
		t.Fatal(err)
	}
	a, err := achrix.New(appConfig(), policy, m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := multisite.NewService(a, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return a, m, s
}

func allow(context.Context, achrix.Principal, string, string) error { return nil }

func start(t *testing.T, a *achrix.Application) {
	t.Helper()
	if err := a.Start(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if err := a.Ready(bounded(t)); err != nil {
		t.Fatal(err)
	}
}

func malformedAuthorities() []string {
	return []string{
		"", " ", "alpha.example ", " alpha.example", "alpha.example\n", "ALPHA.example", "alpha.EXAMPLE", "alpha.example.", ".example", "a..example",
		"https://alpha.example", "//alpha.example", "alpha.example/path", "alpha.example?query", "alpha.example#fragment", "user@alpha.example", "*.example", "a_b.example", "a%2eexample", "a\\b.example", "-a.example", "a-.example", "bücher.example",
		"alpha.example:", "alpha.example:0", "alpha.example:00", "alpha.example:0443", "alpha.example:+443", "alpha.example:65536", "alpha.example:100000", "alpha.example:443:1", "alpha.example:４４３",
		"127.000.0.1", "127.1", "2130706433", "0177.0.0.1", "0x7f000001", "0x7f.0.0.1", "1.0x10", "256.1.1.1", "1.2.3.4.5", "[127.0.0.1]",
		"2001:db8::1", "[2001:DB8::1]", "[2001:0db8::1]", "[2001:db8:0:0:0:0:0:1]", "[2001:db8::01]", "[::ffff:c000:201]", "[fe80::1%eth0]", "[fe80::1%25eth0]", "[2001:db8::1", "[2001:db8::1]]", "[2001:db8::1]extra", "[::1]:", "[::1]:0", "[::1]:0443", "[::1]:65536",
		strings.Repeat("a", 64) + ".example", strings.Repeat("a.", 127) + "example", strings.Repeat("a", 1<<20),
	}
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name   string
		config multisite.Config
	}{
		{"empty", multisite.Config{}},
		{"empty ID", multisite.Config{Sites: []multisite.Site{{Authorities: []string{"a.example"}}}}},
		{"missing authority", multisite.Config{Sites: []multisite.Site{{ID: "a"}}}},
		{"duplicate ID", multisite.Config{Sites: []multisite.Site{{ID: "a", Authorities: []string{"a.example"}}, {ID: "a", Authorities: []string{"b.example"}}}}},
		{"duplicate same site alias", multisite.Config{Sites: []multisite.Site{{ID: "a", Authorities: []string{"a.example", "a.example"}}}}},
		{"duplicate cross site", multisite.Config{Sites: []multisite.Site{{ID: "a", Authorities: []string{"a.example"}}, {ID: "b", Authorities: []string{"a.example"}, Disabled: true}}}},
	}
	for _, id := range []multisite.SiteID{"_a", "-a", "a.b", "a:b", "a b", "é", multisite.SiteID(strings.Repeat("a", 65))} {
		cases = append(cases, struct {
			name   string
			config multisite.Config
		}{"invalid ID " + string(id), multisite.Config{Sites: []multisite.Site{{ID: id, Authorities: []string{"a.example"}}}}})
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if m, err := multisite.New(tt.config); !errors.Is(err, multisite.ErrConfiguration) || m != nil {
				t.Fatalf("New = %v, %v", m, err)
			}
		})
	}
	for _, authority := range malformedAuthorities() {
		if m, err := multisite.New(multisite.Config{Sites: []multisite.Site{{ID: "a", Authorities: []string{authority}}}}); !errors.Is(err, multisite.ErrConfiguration) || m != nil {
			t.Fatalf("malformed authority (length %d): module=%v error=%v", len(authority), m, err)
		}
	}
	for _, authority := range []string{"localhost", "1alpha.example", "a-b.example", "xn--bcher-kva.example", "0xdeadbeef.example", "0.0.0.0", "255.255.255.255:65535", "[::]", "[::1]:1", "[::ffff:192.0.2.1]", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ":65535"} {
		if _, err := multisite.New(multisite.Config{Sites: []multisite.Site{{ID: multisite.SiteID(strings.Repeat("a", 64)), Authorities: []string{authority}}}}); err != nil {
			t.Fatalf("valid authority %q: %v", authority, err)
		}
	}
	// Inventory is explicit finite configuration, not an arbitrary global ceiling.
	config := multisite.Config{}
	for i := range 2049 {
		config.Sites = append(config.Sites, multisite.Site{ID: multisite.SiteID(fmt.Sprintf("site%d", i)), Authorities: []string{fmt.Sprintf("site%d.example", i)}})
	}
	if _, err := multisite.New(config); err != nil {
		t.Fatal(err)
	}
}

func TestExactAuthorizedResolution(t *testing.T) {
	var calls atomic.Int64
	a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(ctx context.Context, p achrix.Principal, c, r string) error {
		calls.Add(1)
		if p != "product-account" || c != multisite.Resolve || r == "" {
			return achrix.ErrDenied
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("missing or excessive operation deadline")
		}
		return nil
	}))
	start(t, a)
	for _, site := range inventory().Sites {
		for _, authority := range site.Authorities {
			id, err := s.Resolve(context.Background(), "product-account", authority)
			if site.Disabled {
				if id != "" || !errors.Is(err, multisite.ErrNotFound) {
					t.Fatalf("disabled: %q, %v", id, err)
				}
			} else if err != nil || id != site.ID {
				t.Fatalf("%q: %q, %v", authority, id, err)
			}
		}
	}
	for _, authority := range []string{"unknown.example", "child.alpha.example", "alpha.example.evil", "beta.example:443", "alpha.example:80", "127.0.0.1:443", "[2001:db8::1]:80"} {
		if id, err := s.Resolve(bounded(t), "product-account", authority); id != "" || !errors.Is(err, multisite.ErrNotFound) {
			t.Fatalf("unknown %q: %q, %v", authority, id, err)
		}
	}
	before := calls.Load()
	for _, authority := range malformedAuthorities() {
		if id, err := s.Resolve(bounded(t), "product-account", authority); id != "" || !errors.Is(err, multisite.ErrInput) {
			t.Fatalf("malformed (length %d): %q, %v", len(authority), id, err)
		}
	}
	if calls.Load() != before {
		t.Fatal("malformed input reached policy")
	}
	if id, err := s.Resolve(bounded(t), "", "alpha.example"); id != "" || !errors.Is(err, achrix.ErrDenied) {
		t.Fatalf("empty principal: %q, %v", id, err)
	}
}

func TestPolicyReceivesExactAuthorityBeforeLookup(t *testing.T) {
	var got []string
	a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(_ context.Context, _ achrix.Principal, c, r string) error {
		if c != multisite.Resolve {
			t.Errorf("capability = %q", c)
		}
		got = append(got, r)
		return achrix.ErrDenied
	}))
	start(t, a)
	want := []string{"alpha.example", "alpha.example:443", "unknown.example", "disabled.example", "[2001:db8::1]:443"}
	for _, authority := range want {
		if id, err := s.Resolve(bounded(t), "actor", authority); id != "" || !errors.Is(err, achrix.ErrDenied) {
			t.Fatalf("%q: %q, %v", authority, id, err)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("resources=%v", got)
	}
}

func TestPolicyFailureIsSafe(t *testing.T) {
	a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		return errors.New("restricted-provider-diagnostic")
	}))
	start(t, a)
	id, err := s.Resolve(bounded(t), "actor", "alpha.example")
	if id != "" || !errors.Is(err, achrix.ErrAuthorizationUnavailable) || strings.Contains(err.Error(), "restricted") {
		t.Fatalf("failure: %q, %v", id, err)
	}
}

func TestCopiedInventoryAndDescriptor(t *testing.T) {
	config := inventory()
	a, m, s := compose(t, config, achrix.PolicyFunc(allow))
	config.Sites[0].ID = "changed"
	config.Sites[0].Authorities[0] = "changed.example"
	config.Sites[0].Disabled = true
	config.Sites[1] = multisite.Site{ID: "replacement", Authorities: []string{"replacement.example"}}
	d := m.Descriptor()
	d.Provides[0].ID = "changed.resolve"
	d.Requires[0].Version = 1
	start(t, a)
	if id, err := s.Resolve(bounded(t), "actor", "alpha.example"); err != nil || id != "Alpha_1" {
		t.Fatalf("copy: %q, %v", id, err)
	}
	if id, err := s.Resolve(bounded(t), "actor", "beta.example"); err != nil || id != "Beta-2" {
		t.Fatalf("copy: %q, %v", id, err)
	}
	if id, err := s.Resolve(bounded(t), "actor", "changed.example"); id != "" || !errors.Is(err, multisite.ErrNotFound) {
		t.Fatalf("mutated alias: %q, %v", id, err)
	}
	fresh := m.Descriptor()
	if fresh.ID != "achrix.multisite" || fresh.Version != achrix.Version() || len(fresh.Provides) != 1 || fresh.Provides[0] != (achrix.Capability{ID: multisite.Resolve, Version: 1}) || len(fresh.Requires) != 1 || fresh.Requires[0] != (achrix.Capability{ID: "achrix.authorization", Version: 2}) || len(fresh.Optional) != 0 {
		t.Fatalf("descriptor: %+v", fresh)
	}
}

func TestContextsAndOneSecondBudget(t *testing.T) {
	for _, tt := range []struct {
		name     string
		canceled bool
	}{{"canceled", true}, {"expired", false}} {
		t.Run(tt.name, func(t *testing.T) {
			var called atomic.Bool
			a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { called.Store(true); return nil }))
			start(t, a)
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.DeadlineExceeded
			if tt.canceled {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
				want = context.Canceled
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			if id, err := s.Resolve(ctx, "actor", "alpha.example"); id != "" || !errors.Is(err, want) || called.Load() {
				t.Fatalf("context: %q, %v, called=%v", id, err, called.Load())
			}
		})
	}
	a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _, _ string) error { <-ctx.Done(); return nil }))
	start(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if id, err := s.Resolve(ctx, "actor", "alpha.example"); id != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("earlier deadline: %q, %v", id, err)
	}
	began := time.Now()
	if id, err := s.Resolve(context.Background(), "actor", "alpha.example"); id != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("maximum deadline: %q, %v", id, err)
	}
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("one-second operation took %v", elapsed)
	}
}

func TestCancellationAtPolicyReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(bounded(t))
	defer cancel()
	a, _, s := compose(t, inventory(), achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { cancel(); return nil }))
	start(t, a)
	if id, err := s.Resolve(ctx, "actor", "alpha.example"); id != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled allow: %q, %v", id, err)
	}
}

func TestModuleLifecycle(t *testing.T) {
	a, m, s := compose(t, inventory(), achrix.PolicyFunc(allow))
	if id, err := s.Resolve(bounded(t), "actor", "alpha.example"); id != "" || !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatalf("before start: %q, %v", id, err)
	}
	if err := m.Start(context.Background()); !errors.Is(err, multisite.ErrConfiguration) {
		t.Fatalf("unbounded start: %v", err)
	}
	start(t, a)
	if err := m.Start(bounded(t)); !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatalf("repeat start: %v", err)
	}
	if err := m.Ready(context.Background()); !errors.Is(err, multisite.ErrConfiguration) {
		t.Fatalf("unbounded readiness: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stop: %v", err)
	}
	if err := m.Ready(bounded(t)); !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatalf("stopped readiness: %v", err)
	}
	if id, err := s.Resolve(bounded(t), "actor", "alpha.example"); id != "" || !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatalf("stopped: %q, %v", id, err)
	}
	if err := m.Stop(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(bounded(t)); !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatalf("restart: %v", err)
	}
}

func TestModuleStopWhilePolicyRuns(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, m, s := compose(t, inventory(), achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _, _ string) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	start(t, a)
	result := make(chan error, 1)
	go func() {
		id, err := s.Resolve(bounded(t), "actor", "alpha.example")
		if id != "" {
			result <- fmt.Errorf("stopped resolution returned %q", id)
			return
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("policy did not start")
	}
	if err := m.Stop(bounded(t)); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, multisite.ErrUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resolution did not finish")
	}
}

func TestApplicationShutdownCancelsResolution(t *testing.T) {
	entered := make(chan struct{})
	a, m, s := compose(t, inventory(), achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _, _ string) error {
		close(entered)
		<-ctx.Done()
		return nil
	}))
	start(t, a)
	result := make(chan error, 1)
	go func() {
		id, err := s.Resolve(bounded(t), "actor", "alpha.example")
		if id != "" {
			result <- fmt.Errorf("shutdown returned %q", id)
			return
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("policy did not start")
	}
	if err := a.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resolution did not cancel")
	}
	if err := m.Ready(bounded(t)); !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatal(err)
	}
	if id, err := s.Resolve(bounded(t), "actor", "alpha.example"); id != "" || !(errors.Is(err, achrix.ErrNotReady) || errors.Is(err, multisite.ErrUnavailable)) {
		t.Fatalf("after shutdown: %q, %v", id, err)
	}
}

func TestConcurrentStartStopAndResolution(t *testing.T) {
	ctx := bounded(t)
	for range 100 {
		m, err := multisite.New(inventory())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := m.Start(ctx)
			if err != nil && !errors.Is(err, multisite.ErrUnavailable) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := m.Stop(ctx); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		if err := m.Ready(ctx); !errors.Is(err, multisite.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	a, m, s := compose(t, inventory(), achrix.PolicyFunc(allow))
	start(t, a)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				id, err := s.Resolve(ctx, "actor", "alpha.example")
				if err == nil {
					if id != "Alpha_1" {
						t.Errorf("wrong site %q", id)
					}
				} else if id != "" || !errors.Is(err, multisite.ErrUnavailable) {
					t.Errorf("concurrent: %q, %v", id, err)
				}
			}
		}()
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for range 10 {
		if id, err := s.Resolve(ctx, "actor", "alpha.example"); id != "" || !errors.Is(err, multisite.ErrUnavailable) {
			t.Fatalf("after stop: %q, %v", id, err)
		}
	}
}

// optionalConsumer is an actual Core participant with product-local behavior;
// omission selects its explicit single-site identity without a resolver/provider.
type optionalConsumer struct{ service *multisite.Service }

func (*optionalConsumer) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "example.consumer", Version: "test", Optional: []achrix.Capability{{ID: multisite.Resolve, Version: 1}}}
}
func (*optionalConsumer) Start(ctx context.Context) error { return ctx.Err() }
func (*optionalConsumer) Ready(ctx context.Context) error { return ctx.Err() }
func (*optionalConsumer) Stop(ctx context.Context) error  { return ctx.Err() }
func (c *optionalConsumer) site(ctx context.Context, authority string) (multisite.SiteID, error) {
	if c.service == nil {
		return "single-site", nil
	}
	return c.service.Resolve(ctx, "actor", authority)
}

func TestIndependentInstancesAndOptionalComposition(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		consumer := &optionalConsumer{}
		var modules []achrix.Module
		modules = append(modules, consumer)
		var m *multisite.Module
		if enabled {
			var err error
			m, err = multisite.New(inventory())
			if err != nil {
				t.Fatal(err)
			}
			modules = append(modules, m)
		}
		a, err := achrix.New(appConfig(), achrix.PolicyFunc(allow), modules...)
		if err != nil {
			t.Fatal(err)
		}
		if enabled {
			consumer.service, err = multisite.NewService(a, m)
			if err != nil {
				t.Fatal(err)
			}
			if a.Components()[0].ID != "achrix.multisite" {
				t.Fatal("provider not ordered before optional consumer")
			}
		}
		start(t, a)
		id, err := consumer.site(bounded(t), "alpha.example")
		want := multisite.SiteID("single-site")
		if enabled {
			want = "Alpha_1"
		}
		if err != nil || id != want {
			t.Fatalf("enabled=%v: %q, %v", enabled, id, err)
		}
		if err := a.Shutdown(bounded(t)); err != nil {
			t.Fatal(err)
		}
	}
	first, firstModule, firstService := compose(t, multisite.Config{Sites: []multisite.Site{{ID: "first", Authorities: []string{"shared.example"}}}}, achrix.PolicyFunc(allow))
	second, _, secondService := compose(t, multisite.Config{Sites: []multisite.Site{{ID: "second", Authorities: []string{"shared.example"}}}}, achrix.PolicyFunc(allow))
	start(t, first)
	start(t, second)
	if id, err := firstService.Resolve(bounded(t), "actor", "shared.example"); err != nil || id != "first" {
		t.Fatalf("first: %q, %v", id, err)
	}
	if err := firstModule.Stop(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if id, err := secondService.Resolve(bounded(t), "actor", "shared.example"); err != nil || id != "second" {
		t.Fatalf("independent: %q, %v", id, err)
	}
}

func TestServiceConstructionAndCoreAdmission(t *testing.T) {
	a, m, _ := compose(t, inventory(), achrix.PolicyFunc(allow))
	for _, pair := range []struct {
		app    *achrix.Application
		module *multisite.Module
	}{{nil, m}, {a, nil}, {a, &multisite.Module{}}} {
		if s, err := multisite.NewService(pair.app, pair.module); s != nil || !errors.Is(err, multisite.ErrConfiguration) {
			t.Fatalf("invalid service: %v, %v", s, err)
		}
	}
	// A ready Module alone cannot bypass admission of its owning Core Application.
	if err := m.Start(bounded(t)); err != nil {
		t.Fatal(err)
	}
	s, err := multisite.NewService(a, m)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.Resolve(bounded(t), "actor", "alpha.example"); id != "" || !errors.Is(err, achrix.ErrNotReady) {
		t.Fatalf("Core not ready: %q, %v", id, err)
	}
}

func TestCanceledModuleCallbacks(t *testing.T) {
	m, err := multisite.New(inventory())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup: %v", err)
	}
	if err := m.Start(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled readiness: %v", err)
	}
	if err := m.Stop(bounded(t)); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationStartupShutdownRace(t *testing.T) {
	ctx := bounded(t)
	for range 50 {
		a, _, s := compose(t, inventory(), achrix.PolicyFunc(allow))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := a.Start(ctx)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, achrix.ErrNotReady) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := a.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		if id, err := s.Resolve(ctx, "actor", "alpha.example"); id != "" || !(errors.Is(err, multisite.ErrUnavailable) || errors.Is(err, achrix.ErrNotReady)) {
			t.Fatalf("startup/shutdown: %q, %v", id, err)
		}
	}
}
