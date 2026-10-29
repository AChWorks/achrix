// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
	app, module, service := managedService(t, base, settings, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
		targets = append(targets, target)
		if actor == "reader" && capability == multisite.InventoryRead && (target == multisite.InventoryTarget || target == "a" || target == "c") {
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
	for _, id := range []multisite.SiteID{"b", "disabled", "unknown"} {
		if site, err := service.Site(context.Background(), "reader", id); site.ID != "" || !errors.Is(err, achrix.ErrDenied) {
			t.Fatalf("unauthorized site %s: %+v %v", id, site, err)
		}
	}
	if _, err := service.Inventory(context.Background(), "stranger", "", 100); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal(err)
	}
	if _, err := service.Inventory(context.Background(), "reader", "not canonical!", 1); !errors.Is(err, multisite.ErrInput) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := service.Inventory(context.Background(), "reader", "", limit); !errors.Is(err, multisite.ErrInput) {
			t.Fatal(err)
		}
	}
}

func TestManagedReadFailureReturnsNoPartialPage(t *testing.T) {
	base, settings := managedConfig()
	app, _, service := managedService(t, base, settings, achrix.PolicyFunc(func(_ context.Context, _ achrix.Principal, _ string, target string) error {
		if target == "b" {
			return errors.New("private policy failure")
		}
		return nil
	}))
	start(t, app)
	page, err := service.Inventory(context.Background(), "actor", "", 100)
	if !errors.Is(err, achrix.ErrAuthorizationUnavailable) || len(page.Sites) != 0 || page.NextCursor != "" || strings.Contains(err.Error(), "private") {
		t.Fatalf("partial or unsafe failure: %+v %v", page, err)
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
	for _, operation := range []string{"inventory", "plan"} {
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
				if operation == "inventory" {
					page, err := service.Inventory(context.Background(), "operator", "", 1)
					if len(page.Sites) > 0 {
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
