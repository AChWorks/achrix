// SPDX-License-Identifier: MPL-2.0

// Package achrix supplies instance-owned composition, authorization and lifecycle
// for trusted, compiled modules. It owns no product data or authentication scheme.
package achrix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

var (
	ErrDenied      = errors.New("permission denied")
	ErrNotReady    = errors.New("application not ready")
	ErrComposition = errors.New("invalid composition")
)

// Capability identifies an owned public contract. Version is its positive ABI
// revision, not the implementation's release version.
type Capability struct {
	ID      string
	Version uint32
}

type Descriptor struct {
	ID       string
	Version  string
	Provides []Capability
	Requires []Capability
}

// Module owns its resources. Start, Ready and Stop must honor context cancellation.
// Stop must clean resources acquired by a partially failed Start and be safe when
// Start acquired none. In-process code is trusted; contexts are not a sandbox.
type Module interface {
	Descriptor() Descriptor
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

// Principal is a product-authenticated opaque identity, never a bearer token.
type Principal string

// Policy is the consumer-owned authorization decision. Any non-nil result denies.
// It must honor context cancellation and must not mutate domain state.
type Policy interface {
	Authorize(context.Context, Principal, string, string) error
}
type PolicyFunc func(context.Context, Principal, string, string) error

func (f PolicyFunc) Authorize(ctx context.Context, p Principal, c, r string) error {
	return f(ctx, p, c, r)
}

type Config struct {
	// Timeouts bound the whole lifecycle phase, including all participating modules.
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

type component struct {
	module     Module
	descriptor Descriptor
}

// Application is immutable after composition except for its serialized lifecycle.
// Domain methods remain typed consumer-owned methods, not an untyped dispatcher.
type Application struct {
	mu           sync.RWMutex
	config       Config
	policy       Policy
	components   []component
	capabilities map[string]Capability
	state        string
}

var identity = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

// New rejects duplicate, missing, incompatible or cyclic required contracts before
// any module starts. Module descriptors are snapshotted to prevent mutable registry
// state leaking between instances. No process-global registration exists.
func New(config Config, policy Policy, modules ...Module) (*Application, error) {
	if nilValue(policy) || config.StartupTimeout <= 0 || config.ShutdownTimeout <= 0 || config.StartupTimeout > time.Minute || config.ShutdownTimeout > time.Minute {
		return nil, fmt.Errorf("%w: policy and lifecycle bounds required", ErrComposition)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	a := &Application{config: config, policy: policy, state: "new", capabilities: map[string]Capability{"achrix.authorization": {ID: "achrix.authorization", Version: 1}}}
	owners := map[string]int{"achrix.authorization": -1}
	ids := map[string]bool{}
	all := make([]component, len(modules))
	for i, m := range modules {
		if nilValue(m) {
			return nil, fmt.Errorf("%w: nil module", ErrComposition)
		}
		d := clone(m.Descriptor())
		if !identity.MatchString(d.ID) || d.Version == "" || ids[d.ID] {
			return nil, fmt.Errorf("%w: module identity", ErrComposition)
		}
		ids[d.ID] = true
		all[i] = component{m, d}
		for _, c := range d.Provides {
			if !valid(c) {
				return nil, fmt.Errorf("%w: capability identity", ErrComposition)
			}
			if _, ok := owners[c.ID]; ok {
				return nil, fmt.Errorf("%w: duplicate capability %s", ErrComposition, c.ID)
			}
			owners[c.ID] = i
			a.capabilities[c.ID] = c
		}
	}
	// Each dependency edge appears once even when a provider owns several required
	// capabilities. Stable input ordering breaks ties, never package init order.
	deps := make([]map[int]bool, len(all))
	for i, c := range all {
		deps[i] = map[int]bool{}
		seen := map[string]bool{}
		for _, r := range c.descriptor.Requires {
			got, ok := a.capabilities[r.ID]
			if !valid(r) || !ok || got.Version != r.Version || seen[r.ID] {
				return nil, fmt.Errorf("%w: required capability %s", ErrComposition, r.ID)
			}
			seen[r.ID] = true
			if owner := owners[r.ID]; owner >= 0 {
				deps[i][owner] = true
			}
		}
	}
	used := make([]bool, len(all))
	for len(a.components) < len(all) {
		progress := false
		for i, c := range all {
			if used[i] {
				continue
			}
			ready := true
			for j := range deps[i] {
				if !used[j] {
					ready = false
					break
				}
			}
			if ready {
				a.components = append(a.components, c)
				used[i] = true
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("%w: startup dependency cycle", ErrComposition)
		}
	}
	return a, nil
}

func valid(c Capability) bool { return identity.MatchString(c.ID) && c.Version > 0 }
func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return r.IsNil()
	}
	return false
}
func clone(d Descriptor) Descriptor {
	d.Provides = slices.Clone(d.Provides)
	d.Requires = slices.Clone(d.Requires)
	return d
}

// Start is a one-shot operation. A failed start cleans the failing module and all
// earlier modules in reverse order using a fresh bounded cleanup context. Lifecycle
// failures are diagnosed by component and phase without logging extension errors.
func (a *Application) Start(parent context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != "new" {
		return ErrNotReady
	}
	a.state = "starting"
	ctx, cancel := context.WithTimeout(parent, a.config.StartupTimeout)
	defer cancel()
	for i, c := range a.components {
		err := ctx.Err()
		if err == nil {
			err = c.module.Start(ctx)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			a.config.Logger.ErrorContext(parent, "lifecycle failure", "component", c.descriptor.ID, "phase", "start", "reason", reason(err))
			a.state = "stopped"
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), a.config.ShutdownTimeout)
			defer cancel()
			return errors.Join(fmt.Errorf("component %s start: %w", c.descriptor.ID, err), a.stop(cleanup, a.components[:i+1]))
		}
	}
	a.state = "ready"
	a.config.Logger.InfoContext(parent, "application ready", "component", "achrix.core", "core_version", Version())
	return nil
}

// Shutdown prevents new authorization and stops started modules in reverse order.
// Repeated shutdown is safe. Modules must wait for/terminate their owned work in Stop.
func (a *Application) Shutdown(parent context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == "stopped" {
		return nil
	}
	if a.state == "new" {
		a.state = "stopped"
		return nil
	}
	a.state = "stopped"
	ctx, cancel := context.WithTimeout(parent, a.config.ShutdownTimeout)
	defer cancel()
	return a.stop(ctx, a.components)
}

func (a *Application) stop(ctx context.Context, components []component) error {
	var errs []error
	for i := len(components) - 1; i >= 0; i-- {
		c := components[i]
		if err := c.module.Stop(ctx); err != nil {
			a.config.Logger.ErrorContext(ctx, "lifecycle failure", "component", c.descriptor.ID, "phase", "stop", "reason", reason(err))
			errs = append(errs, fmt.Errorf("component %s stop: %w", c.descriptor.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Ready checks only composed local dependencies, under the caller's deadline.
// During lifecycle exclusion it returns ErrNotReady without waiting or invoking modules.
func (a *Application) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !a.mu.TryRLock() {
		return ErrNotReady
	}
	defer a.mu.RUnlock()
	if a.state != "ready" {
		return ErrNotReady
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("readiness deadline required")
	}
	for _, c := range a.components {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.module.Ready(ctx); err != nil {
			a.config.Logger.WarnContext(ctx, "readiness failure", "component", c.descriptor.ID, "reason", reason(err))
			return fmt.Errorf("component %s readiness: %w", c.descriptor.ID, err)
		}
	}
	return ctx.Err()
}

// Authorize fails closed for empty principals, unknown capabilities, stopped
// applications or policy errors. Call it inside the owning Application operation
// before validation-dependent reads/writes; transport discovery grants no rights.
// During lifecycle exclusion it returns ErrNotReady without waiting or invoking policy.
func (a *Application) Authorize(ctx context.Context, p Principal, capability, resource string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !a.mu.TryRLock() {
		return ErrNotReady
	}
	defer a.mu.RUnlock()
	if a.state != "ready" {
		return ErrNotReady
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := a.capabilities[capability]; !ok || p == "" {
		a.config.Logger.WarnContext(ctx, "authorization denied", "component", "achrix.authorization", "reason", "unknown_capability_or_principal")
		return ErrDenied
	}
	if err := a.policy.Authorize(ctx, p, capability, resource); err != nil {
		a.config.Logger.WarnContext(ctx, "authorization denied", "component", "achrix.authorization", "reason", "policy_denied")
		return ErrDenied
	}
	return ctx.Err()
}

// Components returns a defensive snapshot for compatible build/recovery identity.
func (a *Application) Components() []Descriptor {
	result := make([]Descriptor, 0, len(a.components))
	for _, c := range a.components {
		result = append(result, clone(c.descriptor))
	}
	return result
}

// Version identifies the actual source-backed Foundation dependency in a consumer.
// Local unversioned builds explicitly report development; no release is invented.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "development"
	}
	if info.Main.Path == "github.com/AChWorks/achrix" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/AChWorks/achrix" {
			if d.Replace != nil {
				return "development-replacement"
			}
			return d.Version
		}
	}
	return "development"
}

func reason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "component_failure"
}
