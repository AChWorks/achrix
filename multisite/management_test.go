// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
)

func managedConfig() (multisite.Config, multisite.ManagementConfig) {
	base := multisite.Config{Sites: []multisite.Site{
		{ID: "a", Authorities: []string{"a.example"}},
		{ID: "b", Authorities: []string{"b.example"}},
		{ID: "disabled", Authorities: []string{"disabled.example"}, Disabled: true},
	}}
	settings := multisite.ManagementConfig{
		Available: []achrix.Descriptor{
			{ID: "example.store", Version: "v1.2.0", Provides: []achrix.Capability{{ID: "example.store", Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}},
			{ID: "example.search", Version: "v0.3.0", Provides: []achrix.Capability{{ID: "example.search", Version: 1}}, Requires: []achrix.Capability{{ID: "example.store", Version: 1}, {ID: "achrix.authorization", Version: 2}}},
		},
		Sites: []multisite.SiteSelection{
			{ID: "a", Modules: []string{"example.store", "example.search"}},
			{ID: "b", Modules: []string{"example.store"}},
		},
	}
	return base, settings
}

func managedService(t *testing.T, base multisite.Config, settings multisite.ManagementConfig, policy achrix.Policy) (*achrix.Application, *multisite.Module, *multisite.Service) {
	t.Helper()
	module, err := multisite.NewManaged(base, settings)
	if err != nil {
		t.Fatal(err)
	}
	app, err := achrix.New(appConfig(), policy, module)
	if err != nil {
		t.Fatal(err)
	}
	service, err := multisite.NewService(app, module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(bounded(t)); err != nil {
			t.Error(err)
		}
	})
	return app, module, service
}

func TestManagedCatalogueAndSelectionsValidatedBeforeResources(t *testing.T) {
	tests := map[string]func(*multisite.Config, *multisite.ManagementConfig){
		"duplicate module": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available = append(m.Available, m.Available[0])
		},
		"invalid module":                 func(_ *multisite.Config, m *multisite.ManagementConfig) { m.Available[0].ID = "bad id" },
		"missing implementation version": func(_ *multisite.Config, m *multisite.ManagementConfig) { m.Available[0].Version = "" },
		"reserved core capability": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available[0].Provides[0].ID = "achrix.authorization"
		},
		"duplicate requirement": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available[1].Optional = []achrix.Capability{{ID: "example.store", Version: 1}}
		},
		"unmet dependency": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Sites[0].Modules = []string{"example.search"}
		},
		"wrong ABI": func(_ *multisite.Config, m *multisite.ManagementConfig) { m.Available[1].Requires[0].Version = 2 },
		"required cycle": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available[0].Requires = append(m.Available[0].Requires, achrix.Capability{ID: "example.search", Version: 1})
		},
		"optional cycle": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available[0].Optional = []achrix.Capability{{ID: "example.search", Version: 1}}
		},
		"two providers": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Available[1].Provides = []achrix.Capability{{ID: "example.store", Version: 1}}
		},
		"unknown selected module": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Sites[0].Modules = []string{"example.unknown"}
		},
		"duplicate selected module": func(_ *multisite.Config, m *multisite.ManagementConfig) {
			m.Sites[0].Modules = []string{"example.store", "example.store"}
		},
		"unknown selected site":   func(_ *multisite.Config, m *multisite.ManagementConfig) { m.Sites[0].ID = "unknown" },
		"duplicate selected site": func(_ *multisite.Config, m *multisite.ManagementConfig) { m.Sites[1].ID = "a" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			base, settings := managedConfig()
			change(&base, &settings)
			if module, err := multisite.NewManaged(base, settings); module != nil || !errors.Is(err, multisite.ErrConfiguration) {
				t.Fatalf("invalid configuration accepted: module=%v err=%v", module, err)
			}
		})
	}
	base, _ := managedConfig()
	if _, err := multisite.NewManaged(base, multisite.ManagementConfig{}); err != nil {
		t.Fatalf("empty optional selection rejected: %v", err)
	}
}

func TestManagedInventoryAuthorizesExactSitesWithoutLeakingOtherSites(t *testing.T) {
	base, settings := managedConfig()
	base.Sites = append(base.Sites, multisite.Site{ID: "c", Authorities: []string{"c.private.example"}})
	var targets []string
	allowC := true
	app, module, service := managedService(t, base, settings, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
		targets = append(targets, target)
		if actor == "reader" && capability == multisite.InventoryRead && (target == multisite.InventoryTarget || target == "a" || target == "c" && allowC) {
			return nil
		}
		return achrix.ErrDenied
	}))
	start(t, app)
	descriptor := module.Descriptor()
	if len(descriptor.Provides) != 3 || descriptor.Provides[1].ID != multisite.InventoryRead || descriptor.Provides[2].ID != multisite.PlanActivation {
		t.Fatalf("managed capabilities: %+v", descriptor.Provides)
	}
	page, err := service.Inventory(context.Background(), "reader", "", 1)
	if err != nil || len(page.Sites) != 1 || page.Sites[0].ID != "a" || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if strings.Contains(fmt.Sprint(page), "private") || strings.Contains(fmt.Sprint(page), "b.example") {
		t.Fatal("inventory leaked authorities")
	}
	second, err := service.Inventory(context.Background(), "reader", page.NextCursor, 1)
	if err != nil || len(second.Sites) != 1 || second.Sites[0].ID != "c" || second.NextCursor != "" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	if len(targets) == 0 || targets[0] != multisite.InventoryTarget {
		t.Fatal("collection permission not checked first")
	}
	allowC = false
	if revoked, err := service.Inventory(context.Background(), "reader", page.NextCursor, 1); err != nil || len(revoked.Sites) != 0 || revoked.NextCursor != "" {
		t.Fatalf("cursor retained a revoked site grant: %+v %v", revoked, err)
	}
	for _, id := range []multisite.SiteID{"b", "disabled", "unknown"} {
		if site, err := service.Site(context.Background(), "reader", id); site.ID != "" || !errors.Is(err, achrix.ErrDenied) {
			t.Fatalf("unauthorized site %s: %+v %v", id, site, err)
		}
	}
	for _, cursor := range []string{"", base64.RawURLEncoding.EncodeToString([]byte("z"))} {
		targets = nil
		page, err := service.Inventory(context.Background(), "stranger", cursor, 100)
		if !errors.Is(err, achrix.ErrDenied) || len(page.Sites) != 0 || page.NextCursor != "" || !slices.Equal(targets, []string{multisite.InventoryTarget}) {
			t.Fatalf("collection denial bypassed for cursor %q: %+v %v targets=%v", cursor, page, err, targets)
		}
	}
	targets = nil
	if _, err := service.Inventory(context.Background(), "reader", "private-cursor!", 1); !errors.Is(err, multisite.ErrInput) || !strings.Contains(err.Error(), "cursor") || strings.Contains(err.Error(), "private-cursor") || len(targets) != 0 {
		t.Fatalf("invalid cursor lacks safe field guidance or called policy: %v targets=%v", err, targets)
	}
	for _, limit := range []int{0, 101} {
		if _, err := service.Inventory(context.Background(), "reader", "", limit); !errors.Is(err, multisite.ErrInput) || !strings.Contains(err.Error(), "limit: choose 1..100") || len(targets) != 0 {
			t.Fatalf("invalid limit lacks actionable bounds or called policy: %v targets=%v", err, targets)
		}
	}
}

func TestManagedInventoryCursorBoundariesAndSparseGrants(t *testing.T) {
	base := multisite.Config{}
	// Deliberately unsorted, with case-sensitive ordering and a routing-disabled
	// site that remains readable when inventory permission permits it.
	for i, id := range []multisite.SiteID{"f", "a", "B", "e", "c", "d"} {
		base.Sites = append(base.Sites, multisite.Site{ID: id, Authorities: []string{fmt.Sprintf("site-%d.private.example", i)}, Disabled: id == "e"})
	}
	for _, tt := range []struct {
		name, after string
		limit       int
		allowed     []string // nil permits all; an empty non-nil slice permits none.
		want        []string
		cursor      string
		targets     []string
	}{
		{name: "first", limit: 2, want: []string{"B", "a"}, cursor: "a", targets: []string{"", "B", "a", "c"}},
		{name: "before first", after: "A", limit: 1, want: []string{"B"}, cursor: "B", targets: []string{"", "B", "a"}},
		{name: "equal first", after: "B", limit: 2, want: []string{"a", "c"}, cursor: "c", targets: []string{"", "a", "c", "d"}},
		{name: "unknown between IDs", after: "b", limit: 2, want: []string{"c", "d"}, cursor: "d", targets: []string{"", "c", "d", "e"}},
		{name: "middle", after: "c", limit: 2, want: []string{"d", "e"}, cursor: "e", targets: []string{"", "d", "e", "f"}},
		{name: "final", after: "e", limit: 1, want: []string{"f"}, targets: []string{"", "f"}},
		{name: "exhausted equal final", after: "f", limit: 1, targets: []string{""}},
		{name: "exhausted past final", after: "z", limit: 1, targets: []string{""}},
		{name: "sparse lookahead", limit: 1, allowed: []string{"a", "e"}, want: []string{"a"}, cursor: "a", targets: []string{"", "B", "a", "c", "d", "e"}},
		{name: "sparse unknown cursor", after: "b", limit: 1, allowed: []string{"a", "e"}, want: []string{"e"}, targets: []string{"", "c", "d", "e", "f"}},
		{name: "denied suffix", after: "a", limit: 1, allowed: []string{"a", "c"}, want: []string{"c"}, targets: []string{"", "c", "d", "e", "f"}},
		{name: "all denied", limit: 1, allowed: []string{}, targets: []string{"", "B", "a", "c", "d", "e", "f"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var targets []string
			app, _, service := managedService(t, base, multisite.ManagementConfig{}, achrix.PolicyFunc(func(ctx context.Context, actor achrix.Principal, capability, target string) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				targets = append(targets, target)
				if actor != "reader" || capability != multisite.InventoryRead {
					return achrix.ErrDenied
				}
				if target == multisite.InventoryTarget || tt.allowed == nil || slices.Contains(tt.allowed, target) {
					return nil
				}
				return achrix.ErrDenied
			}))
			start(t, app)
			cursor := base64.RawURLEncoding.EncodeToString([]byte(tt.after))
			page, err := service.Inventory(context.Background(), "reader", cursor, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, site := range page.Sites {
				ids = append(ids, string(site.ID))
			}
			if !slices.Equal(ids, tt.want) || !slices.Equal(targets, tt.targets) {
				t.Fatalf("page=%v want=%v; policy targets=%v want=%v", ids, tt.want, targets, tt.targets)
			}
			decoded, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
			if err != nil || string(decoded) != tt.cursor {
				t.Fatalf("cursor must contain only the last returned authorized ID: %q %v, want %q", decoded, err, tt.cursor)
			}
			if strings.Contains(fmt.Sprint(page), "private.example") {
				t.Fatal("page disclosed authority bindings")
			}
		})
	}
}

func TestManagedReadFailureReturnsNoPartialPage(t *testing.T) {
	base, settings := managedConfig()
	module, err := multisite.NewManaged(base, settings)
	if err != nil {
		t.Fatal(err)
	}
	policyErr := errors.New("private policy failure")
	type correlationKey struct{}
	ctx := context.WithValue(context.Background(), correlationKey{}, "inventory-operation")
	var diagnosticCount int
	cfg := appConfig()
	cfg.Diagnostic = func(ctx context.Context, operation, phase string, cause error) {
		diagnosticCount++
		if operation != "authorize" || phase != "policy" || !errors.Is(cause, policyErr) || ctx.Value(correlationKey{}) != "inventory-operation" {
			t.Errorf("protected diagnosis lost cause, phase or correlation: %s %s %v", operation, phase, cause)
		}
	}
	app, err := achrix.New(cfg, achrix.PolicyFunc(func(_ context.Context, _ achrix.Principal, _ string, target string) error {
		if target == "b" {
			return policyErr
		}
		return nil
	}), module)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(bounded(t)); err != nil {
			t.Error(err)
		}
	})
	service, err := multisite.NewService(app, module)
	if err != nil {
		t.Fatal(err)
	}
	start(t, app)
	// Limit 1 fails during permitted lookahead after filling the page; limit
	// 100 fails while collecting it. Neither may become denial or partial success.
	for _, limit := range []int{1, 100} {
		diagnosticCount = 0
		page, err := service.Inventory(ctx, "actor", "", limit)
		if !errors.Is(err, achrix.ErrAuthorizationUnavailable) || errors.Is(err, achrix.ErrDenied) || errors.Is(err, policyErr) || len(page.Sites) != 0 || page.NextCursor != "" || strings.Contains(err.Error(), "private") {
			t.Fatalf("limit %d: partial or unsafe failure: %+v %v", limit, page, err)
		}
		if diagnosticCount != 1 {
			t.Fatalf("limit %d: protected diagnostic count = %d, want 1", limit, diagnosticCount)
		}
	}
}

func TestManagedInventoryCancellationDuringDeniedSuffix(t *testing.T) {
	base, settings := managedConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var targets []string
	app, _, service := managedService(t, base, settings, achrix.PolicyFunc(func(_ context.Context, _ achrix.Principal, _ string, target string) error {
		targets = append(targets, target)
		if target == "b" {
			cancel()
			return achrix.ErrDenied
		}
		return nil
	}))
	start(t, app)
	page, err := service.Inventory(ctx, "actor", "", 1)
	if !errors.Is(err, context.Canceled) || len(page.Sites) != 0 || page.NextCursor != "" || !slices.Equal(targets, []string{"", "a", "b"}) {
		t.Fatalf("cancellation lost or partial page returned: %+v %v targets=%v", page, err, targets)
	}
}

func TestManagedInventoryDeadlineIncludesLookahead(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		name := "default one second"
		if earlier {
			name = "earlier caller deadline"
		}
		t.Run(name, func(t *testing.T) {
			base, settings := managedConfig()
			var callerDeadline, operationDeadline time.Time
			var targets []string
			app, _, service := managedService(t, base, settings, achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _ string, target string) error {
				targets = append(targets, target)
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > time.Second {
					t.Error("inventory did not bound policy work to one second")
					return errors.New("unbounded inventory policy context")
				}
				if target == multisite.InventoryTarget {
					operationDeadline = deadline
				}
				if !deadline.Equal(operationDeadline) || earlier && !deadline.Equal(callerDeadline) {
					t.Error("inventory restarted its budget or extended the caller deadline")
				}
				if target == "b" {
					<-ctx.Done()
					return achrix.ErrDenied
				}
				return nil
			}))
			start(t, app)
			ctx := context.Background()
			if earlier {
				callerDeadline = time.Now().Add(250 * time.Millisecond)
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, callerDeadline)
				defer cancel()
			}
			page, err := service.Inventory(ctx, "actor", "", 1)
			if !errors.Is(err, context.DeadlineExceeded) || len(page.Sites) != 0 || page.NextCursor != "" || !slices.Equal(targets, []string{"", "a", "b"}) {
				t.Fatalf("deadline lost or partial page returned: %+v %v targets=%v", page, err, targets)
			}
		})
	}
}

func TestManagedConfigurationPlansAreDefensiveAndTargetOnly(t *testing.T) {
	base, settings := managedConfig()
	app, _, service := managedService(t, base, settings, achrix.PolicyFunc(allow))
	start(t, app)
	before, err := service.Site(context.Background(), "actor", "a")
	if err != nil {
		t.Fatal(err)
	}
	base.Sites[0].ID = "changed"
	settings.Available[0].Provides[0].ID = "changed"
	settings.Sites[0].Modules[0] = "changed"
	if now, err := service.Site(context.Background(), "actor", "a"); err != nil || now.Revision != before.Revision {
		t.Fatal("mutable constructor input changed managed state")
	}
	base, settings = managedConfig()
	request := multisite.ActivationRequest{SiteID: "a", ExpectedRevision: before.Revision, Modules: []string{"example.store"}}
	plan, err := service.PlanActivation(context.Background(), "actor", request)
	if err != nil {
		t.Fatal(err)
	}
	request.Modules[0] = "changed"
	selection := plan.Selection()
	selection.Modules[0] = "changed"
	descriptors := plan.Descriptors()
	descriptors[0].Provides[0].ID = "changed"
	if got := plan.Selection(); len(got.Modules) != 1 || got.Modules[0] != "example.store" {
		t.Fatalf("plan mutable: %+v", got)
	}
	if plan.Site() != "a" || plan.BeforeRevision() != before.Revision || plan.AfterRevision() == before.Revision {
		t.Fatal("plan preconditions absent")
	}
	replacement, err := plan.Replacement(base, settings)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Sites[0].Modules[0] != "example.store" || len(settings.Sites[0].Modules) != 2 {
		t.Fatal("caller configuration mutated")
	}
	replacement.Sites[1].Modules[0] = "changed"
	if settings.Sites[1].Modules[0] != "example.store" {
		t.Fatal("other-site input not defensively copied")
	}
	current, err := service.Site(context.Background(), "actor", "a")
	if err != nil || len(current.Modules) != 2 || current.Revision != before.Revision {
		t.Fatal("plan changed live state")
	}
	current.Modules[0] = "changed"
	if again, _ := service.Site(context.Background(), "actor", "a"); again.Modules[0] == "changed" {
		t.Fatal("SiteInfo mutable")
	}
	page, err := service.Inventory(context.Background(), "actor", "", 1)
	if err != nil || len(page.Sites) != 1 || len(page.Sites[0].Modules) == 0 {
		t.Fatalf("inventory copy setup: %+v %v", page, err)
	}
	page.Sites[0].Modules[0] = "changed"
	if again, _ := service.Site(context.Background(), "actor", "a"); again.Modules[0] == "changed" {
		t.Fatal("inventory shared its Module selection")
	}
}

func TestManagedActivationPermissionAndPreconditions(t *testing.T) {
	base, settings := managedConfig()
	app, _, service := managedService(t, base, settings, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
		if capability == multisite.InventoryRead {
			return nil
		}
		if actor == "operator" && capability == multisite.PlanActivation && target == "a" {
			return nil
		}
		return achrix.ErrDenied
	}))
	start(t, app)
	info, _ := service.Site(context.Background(), "operator", "a")
	request := multisite.ActivationRequest{SiteID: "a", ExpectedRevision: info.Revision, Modules: []string{"example.store"}}
	if _, err := service.PlanActivation(context.Background(), "reader", request); !errors.Is(err, achrix.ErrDenied) {
		t.Fatalf("inventory granted planning: %v", err)
	}
	plan, err := service.PlanActivation(context.Background(), "operator", request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = strings.Repeat("0", 64)
	if _, err := service.PlanActivation(context.Background(), "operator", request); !errors.Is(err, multisite.ErrConflict) {
		t.Fatal(err)
	}
	request.ExpectedRevision = info.Revision
	request.Modules = []string{"example.search"}
	if _, err := service.PlanActivation(context.Background(), "operator", request); !errors.Is(err, multisite.ErrConfiguration) {
		t.Fatalf("dependency removal admitted: %v", err)
	}
	request.Modules = []string{"example.unknown"}
	if _, err := service.PlanActivation(context.Background(), "operator", request); !errors.Is(err, multisite.ErrInput) {
		t.Fatal(err)
	}
	base, changed := managedConfig()
	changed.Available[0].Version = "v1.3.0"
	if _, err := plan.Replacement(base, changed); !errors.Is(err, multisite.ErrConflict) {
		t.Fatalf("catalogue drift admitted: %v", err)
	}
	base, changed = managedConfig()
	changed.Sites[0].Modules = []string{"example.store"}
	if _, err := plan.Replacement(base, changed); !errors.Is(err, multisite.ErrConflict) {
		t.Fatalf("stale target admitted: %v", err)
	}
	base, changed = managedConfig()
	base.Sites[0].Disabled = true
	if _, err := plan.Replacement(base, changed); !errors.Is(err, multisite.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := (multisite.ActivationPlan{}).Replacement(base, changed); !errors.Is(err, multisite.ErrInput) {
		t.Fatal(err)
	}
}

func TestManagedTargetAuthorizationPrecedesExistenceAndCancelledCallsStop(t *testing.T) {
	base, settings := managedConfig()
	app, module, service := managedService(t, base, settings, achrix.PolicyFunc(allow))
	start(t, app)
	for _, id := range []multisite.SiteID{"unknown", "disabled"} {
		if _, err := service.PlanActivation(context.Background(), "actor", multisite.ActivationRequest{SiteID: id, ExpectedRevision: strings.Repeat("0", 64)}); !errors.Is(err, multisite.ErrNotFound) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Site(cancelled, "actor", "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.Inventory(cancelled, "actor", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.PlanActivation(cancelled, "actor", multisite.ActivationRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := module.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inventory(context.Background(), "actor", "", 1); !errors.Is(err, multisite.ErrUnavailable) {
		t.Fatal(err)
	}
}

type retainedFeature struct {
	descriptor       achrix.Descriptor
	data             *atomic.Int64
	started, stopped *atomic.Int64
}

func (m retainedFeature) Descriptor() achrix.Descriptor { return m.descriptor }
func (m retainedFeature) Start(context.Context) error   { m.started.Add(1); return nil }
func (m retainedFeature) Ready(context.Context) error   { return nil }
func (m retainedFeature) Stop(context.Context) error    { m.stopped.Add(1); return nil }

func TestManagedSelectionReconstructionPreservesDataAndSkipsInactiveFactories(t *testing.T) {
	base, settings := managedConfig()
	managementApp, _, service := managedService(t, base, settings, achrix.PolicyFunc(allow))
	start(t, managementApp)
	var constructed, started, stopped atomic.Int64
	var data atomic.Int64
	data.Store(73)
	var buildMu sync.Mutex
	build := func(selection multisite.SiteSelection) *achrix.Application {
		t.Helper()
		buildMu.Lock()
		defer buildMu.Unlock()
		modules := make([]achrix.Module, 0, len(selection.Modules))
		for _, id := range selection.Modules {
			var descriptor achrix.Descriptor
			for _, candidate := range settings.Available {
				if candidate.ID == id {
					descriptor = candidate
				}
			}
			if id == "example.search" {
				constructed.Add(1)
			}
			modules = append(modules, retainedFeature{descriptor: descriptor, data: &data, started: &started, stopped: &stopped})
		}
		app, err := achrix.New(appConfig(), achrix.PolicyFunc(allow), modules...)
		if err != nil {
			t.Fatal(err)
		}
		start(t, app)
		return app
	}
	a := build(settings.Sites[0])
	b := build(settings.Sites[1])
	if constructed.Load() != 1 {
		t.Fatal("inactive Site B feature factory ran")
	}
	if err := a.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Site(context.Background(), "operator", "a")
	off, err := service.PlanActivation(context.Background(), "operator", multisite.ActivationRequest{SiteID: "a", ExpectedRevision: before.Revision, Modules: []string{"example.store"}})
	if err != nil {
		t.Fatal(err)
	}
	without, err := off.Replacement(base, settings)
	if err != nil {
		t.Fatal(err)
	}
	disabled := build(without.Sites[0])
	if constructed.Load() != 1 || data.Load() != 73 {
		t.Fatal("disable constructed feature or deleted retained data")
	}
	if err := disabled.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	nextApp, _, nextService := managedService(t, base, without, achrix.PolicyFunc(allow))
	start(t, nextApp)
	current, _ := nextService.Site(context.Background(), "operator", "a")
	on, err := nextService.PlanActivation(context.Background(), "operator", multisite.ActivationRequest{SiteID: "a", ExpectedRevision: current.Revision, Modules: []string{"example.store", "example.search"}})
	if err != nil {
		t.Fatal(err)
	}
	reenabled, err := on.Replacement(base, without)
	if err != nil {
		t.Fatal(err)
	}
	restored := build(reenabled.Sites[0])
	if constructed.Load() != 2 || data.Load() != 73 {
		t.Fatal("re-enable lost retained state")
	}
	if err := restored.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if err := b.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if started.Load() != stopped.Load() {
		t.Fatalf("lifecycle leaked: start=%d stop=%d", started.Load(), stopped.Load())
	}
}

// Generation is durable product-owned desired state; resetting it would violate
// the supported precondition contract even when the selected IDs happen to match.
func TestManagedGenerationRejectsABAAndOverflow(t *testing.T) {
	base, settings := managedConfig()
	settings.Sites[1].Generation = 17
	app, _, service := managedService(t, base, settings, achrix.PolicyFunc(allow))
	start(t, app)
	info, _ := service.Site(context.Background(), "operator", "a")
	plan, err := service.PlanActivation(context.Background(), "operator", multisite.ActivationRequest{SiteID: "a", ExpectedRevision: info.Revision, Modules: []string{"example.store"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := plan.Replacement(base, settings)
	if err != nil {
		t.Fatal(err)
	}
	if next.Sites[0].Generation != 1 || next.Sites[1].Generation != 17 {
		t.Fatalf("generations not advanced/scoped: %+v", next.Sites)
	}
	aba := settings
	aba.Sites = append([]multisite.SiteSelection(nil), settings.Sites...)
	aba.Sites[0].Generation = 2
	if _, err := plan.Replacement(base, aba); !errors.Is(err, multisite.ErrConflict) {
		t.Fatalf("A -> B -> A revived old plan: %v", err)
	}
	overflow := settings
	overflow.Sites = append([]multisite.SiteSelection(nil), settings.Sites...)
	overflow.Sites[0].Generation = ^uint64(0)
	fullApp, _, fullService := managedService(t, base, overflow, achrix.PolicyFunc(allow))
	start(t, fullApp)
	full, _ := fullService.Site(context.Background(), "operator", "a")
	if _, err := fullService.PlanActivation(context.Background(), "operator", multisite.ActivationRequest{SiteID: "a", ExpectedRevision: full.Revision}); !errors.Is(err, multisite.ErrConflict) {
		t.Fatalf("overflow admitted: %v", err)
	}
	if _, err := plan.Replacement(base, overflow); !errors.Is(err, multisite.ErrConflict) {
		t.Fatalf("overflow replacement admitted: %v", err)
	}
}

func TestManagedReadAndPlanningLinearizeWithStop(t *testing.T) {
	for _, operation := range []string{"inventory", "exhausted inventory", "plan"} {
		t.Run(operation, func(t *testing.T) {
			base, settings := managedConfig()
			entered := make(chan struct{})
			release := make(chan struct{})
			var block atomic.Bool
			app, module, service := managedService(t, base, settings, achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, capability, _ string) error {
				if block.CompareAndSwap(true, false) && (capability == multisite.PlanActivation || capability == multisite.InventoryRead) {
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}))
			start(t, app)
			info, err := service.Site(context.Background(), "operator", "a")
			if err != nil {
				t.Fatal(err)
			}
			block.Store(true)
			result := make(chan error, 1)
			go func() {
				if operation != "plan" {
					cursor := ""
					if operation == "exhausted inventory" {
						cursor = base64.RawURLEncoding.EncodeToString([]byte("disabled"))
					}
					page, err := service.Inventory(context.Background(), "operator", cursor, 1)
					if len(page.Sites) > 0 || page.NextCursor != "" {
						result <- errors.New("stopped inventory leaked result")
						return
					}
					result <- err
					return
				}
				plan, err := service.PlanActivation(context.Background(), "operator", multisite.ActivationRequest{SiteID: "a", ExpectedRevision: info.Revision, Modules: []string{"example.store"}})
				if plan.Site() != "" {
					result <- errors.New("stopped plan returned result")
					return
				}
				result <- err
			}()
			<-entered
			if err := module.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-result; !errors.Is(err, multisite.ErrUnavailable) {
				t.Fatalf("stop race: %v", err)
			}
		})
	}
}
