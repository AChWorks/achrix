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
	ErrDenied                   = errors.New("permission denied")
	ErrAuthorizationUnavailable = errors.New("authorization unavailable")
	ErrNotReady                 = errors.New("application not ready")
	ErrComposition              = errors.New("invalid composition")
)

var errDeadlineRequired = errors.New("operation deadline required")

// Capability identifies an owned public contract. Version is its positive ABI
// revision, not the implementation's release version. Compatible additive changes
// retain the revision; incompatible contracts increment it. Composition matches
// exact revisions and admits only one provider/revision for each capability ID.
type Capability struct {
	ID      string
	Version uint32
}

// Descriptor is deterministic, side-effect-free, cheap composition metadata.
// Describing a Module must not read environment/secrets, acquire resources, call
// networks/databases or mutate registration. Optional providers may be absent;
// when present they must match the exact revision and precede their consumers.
type Descriptor struct {
	ID string

	// Version identifies the Module implementation's packaged source dependency.
	// Official packages in this Go module use Version(); capability ABI is separate.
	Version  string
	Provides []Capability
	Requires []Capability
	Optional []Capability
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

// Policy is the consumer-owned authorization decision: nil allows, ErrDenied
// (possibly wrapped) explicitly denies, and other errors fail closed as evaluation
// unavailable. It must honor cancellation and must not mutate domain state.
// Implementations must be safe for concurrent calls.
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

// Application is immutable after composition except for lifecycle/admission state.
// Domain methods remain typed consumer-owned methods, not an untyped dispatcher.
type Application struct {
	mu           sync.Mutex // Short state/admission sections only; never callbacks.
	config       Config
	policy       Policy
	components   []component
	capabilities map[string]Capability
	state        string
	lifecycle    chan struct{} // Serializes Start/Stop without unbounded lock waits.
	workContext  context.Context
	cancelWork   context.CancelFunc
	active       int
	drained      chan struct{}
	stopErr      error
}

var identity = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

// New rejects duplicate, missing, incompatible or cyclic composed contracts before
// any module starts. Module descriptors are snapshotted to prevent mutable registry
// state leaking between instances. No process-global registration exists.
func New(config Config, policy Policy, modules ...Module) (*Application, error) {
	if nilValue(policy) || config.StartupTimeout <= 0 || config.ShutdownTimeout <= 0 || config.StartupTimeout > time.Minute || config.ShutdownTimeout > time.Minute {
		return nil, fmt.Errorf("%w: policy and lifecycle bounds required", ErrComposition)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	a := &Application{config: config, policy: policy, state: "new", capabilities: map[string]Capability{"achrix.authorization": {ID: "achrix.authorization", Version: 2}}}
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
		for _, group := range []struct {
			capabilities []Capability
			optional     bool
		}{{c.descriptor.Requires, false}, {c.descriptor.Optional, true}} {
			for _, r := range group.capabilities {
				if !valid(r) || seen[r.ID] {
					return nil, fmt.Errorf("%w: dependency declaration %s", ErrComposition, r.ID)
				}
				seen[r.ID] = true
				got, present := a.capabilities[r.ID]
				if !present && group.optional {
					continue
				}
				if !present || got.Version != r.Version {
					return nil, fmt.Errorf("%w: dependency capability %s", ErrComposition, r.ID)
				}
				if owner := owners[r.ID]; owner >= 0 {
					deps[i][owner] = true
				}
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
	a.lifecycle = make(chan struct{}, 1)
	a.drained = make(chan struct{})
	close(a.drained)
	a.workContext, a.cancelWork = context.WithCancel(context.Background())
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
	d.Optional = slices.Clone(d.Optional)
	return d
}

// Start is a one-shot operation. A failed start cleans the failing module and all
// earlier modules in reverse order using a fresh bounded cleanup context. Lifecycle
// failures are diagnosed by component and phase without logging extension errors.
func (a *Application) Start(parent context.Context) error {
	a.mu.Lock()
	if a.state != "new" {
		a.mu.Unlock()
		return ErrNotReady
	}
	// A new instance has no lifecycle owner. Reserve ownership before publishing
	// starting so concurrent Shutdown cannot stop a partially starting Module.
	a.lifecycle <- struct{}{}
	a.state = "starting"
	a.mu.Unlock()
	defer func() { <-a.lifecycle }()
	ctx, cancel := context.WithTimeout(parent, a.config.StartupTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(a.workContext, cancel)
	defer stopCancel()
	var startErr error
	started := 0
	for i, c := range a.components {
		started = i + 1
		err := ctx.Err()
		if err == nil {
			err = c.module.Start(ctx)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			a.config.Logger.ErrorContext(parent, "lifecycle failure", "component", c.descriptor.ID, "operation", "start", "reason", reason(err))
			startErr = fmt.Errorf("component %s start: %w", c.descriptor.ID, err)
			break
		}
	}
	a.mu.Lock()
	if startErr == nil {
		startErr = ctx.Err()
		if startErr == nil && a.state != "starting" {
			startErr = context.Canceled
		}
	}
	if startErr == nil {
		a.state = "ready"
		a.mu.Unlock()
		a.config.Logger.InfoContext(parent, "application ready", "component", "achrix.core", "operation", "start", "core_version", Version())
		return nil
	}
	a.state = "stopping"
	a.mu.Unlock()
	cleanup, cleanupCancel := context.WithTimeout(context.WithoutCancel(parent), a.config.ShutdownTimeout)
	defer cleanupCancel()
	cleanupErr := a.stop(cleanup, a.components[:started])
	a.mu.Lock()
	a.state, a.stopErr = "stopped", cleanupErr
	a.cancelWork()
	a.mu.Unlock()
	return errors.Join(startErr, cleanupErr)
}

// Shutdown closes admission immediately, cancels admitted Ready/Authorize work,
// drains it, then stops Modules in reverse order. Its deadline covers lifecycle
// waiting, drain and Stop together. If waiting/drain expires, the instance remains
// stopping and a later Shutdown may finish cleanup; Stop never races callbacks.
// Completed cleanup is not repeated and its result is retained. Modules must
// terminate their owned domain work in Stop; Core tracks only its own callbacks.
func (a *Application) Shutdown(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, a.config.ShutdownTimeout)
	defer cancel()
	a.mu.Lock()
	if a.state == "stopped" {
		err := a.stopErr
		a.mu.Unlock()
		return err
	}
	if a.state == "new" {
		a.state = "stopped"
		a.cancelWork()
		a.mu.Unlock()
		return nil
	}
	a.state = "stopping"
	a.cancelWork()
	a.mu.Unlock()
	select {
	case a.lifecycle <- struct{}{}:
		defer func() { <-a.lifecycle }()
	case <-ctx.Done():
		return ctx.Err()
	}
	a.mu.Lock()
	if a.state == "stopped" {
		err := a.stopErr
		a.mu.Unlock()
		return err
	}
	drained := a.drained
	a.mu.Unlock()
	select {
	case <-drained:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := a.stop(ctx, a.components)
	a.mu.Lock()
	a.state, a.stopErr = "stopped", err
	a.mu.Unlock()
	return err
}

func (a *Application) stop(ctx context.Context, components []component) error {
	var errs []error
	for i := len(components) - 1; i >= 0; i-- {
		c := components[i]
		if err := c.module.Stop(ctx); err != nil {
			a.config.Logger.ErrorContext(ctx, "lifecycle failure", "component", c.descriptor.ID, "operation", "stop", "reason", reason(err))
			errs = append(errs, fmt.Errorf("component %s stop: %w", c.descriptor.ID, err))
		}
	}
	return errors.Join(append(errs, ctx.Err())...)
}

// Ready checks only composed local dependencies, under the caller's deadline.
// It never waits behind lifecycle callbacks and is canceled/drained by Shutdown.
// Repeated probe detail is DEBUG; the product owns bounded operational signals.
func (a *Application) Ready(ctx context.Context) error {
	ctx, finish, err := a.admit(ctx)
	if err != nil {
		return err
	}
	defer finish()
	for _, c := range a.components {
		if err := a.operationErr(ctx); err != nil {
			return err
		}
		err := c.module.Ready(ctx)
		if canceled := a.operationErr(ctx); canceled != nil {
			return canceled
		}
		if err != nil {
			a.config.Logger.DebugContext(ctx, "readiness failure", "component", c.descriptor.ID, "operation", "ready", "reason", reason(err))
			return fmt.Errorf("component %s readiness: %w", c.descriptor.ID, err)
		}
	}
	return a.operationErr(ctx)
}

// Authorize fails closed for empty principals, unknown capabilities, stopped
// applications or policy errors. Call it inside the owning Application operation
// before authorization-sensitive reads/writes; transport discovery grants no rights.
// resource is a product-owned opaque scope/reference, not trusted client claims.
// Callers must supply a deadline. Shutdown cancels/drains admitted policy work;
// calls outside ready return ErrNotReady without invoking policy. Policy errors
// never expose raw provider text: only explicit ErrDenied is a permission denial.
// Core emits safe DEBUG detail; the product owns bounded denial/failure visibility.
func (a *Application) Authorize(ctx context.Context, p Principal, capability, resource string) error {
	ctx, finish, err := a.admit(ctx)
	if err != nil {
		if errors.Is(err, errDeadlineRequired) {
			return fmt.Errorf("%w: deadline required", ErrAuthorizationUnavailable)
		}
		return err
	}
	defer finish()
	if err := a.operationErr(ctx); err != nil {
		return err
	}
	if _, ok := a.capabilities[capability]; !ok || p == "" {
		a.config.Logger.DebugContext(ctx, "authorization denied", "component", "achrix.authorization", "operation", "authorize", "reason", "unknown_capability_or_principal")
		return ErrDenied
	}
	err = a.policy.Authorize(ctx, p, capability, resource)
	if canceled := a.operationErr(ctx); canceled != nil {
		return canceled
	}
	if errors.Is(err, ErrDenied) {
		a.config.Logger.DebugContext(ctx, "authorization denied", "component", "achrix.authorization", "operation", "authorize", "reason", "policy_denied")
		return ErrDenied
	}
	if err != nil {
		a.config.Logger.DebugContext(ctx, "authorization unavailable", "component", "achrix.authorization", "operation", "authorize", "reason", "policy_evaluation_failed")
		return ErrAuthorizationUnavailable
	}
	return nil
}

// admit/finish protect Module resources until every Core callback returns. No
// extension callback or diagnostic handler executes while the state lock is held.
func (a *Application) admit(parent context.Context) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != "ready" {
		return nil, nil, ErrNotReady
	}
	if _, ok := parent.Deadline(); !ok {
		return nil, nil, errDeadlineRequired
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stopCancel := context.AfterFunc(a.workContext, cancel)
	if a.active == 0 {
		a.drained = make(chan struct{})
	}
	a.active++
	return ctx, func() {
		stopCancel()
		cancel()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.active--
		if a.active == 0 {
			close(a.drained)
		}
	}, nil
}

func (a *Application) operationErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// AfterFunc cancellation may still be scheduled: never allow during that gap.
	return a.workContext.Err()
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
	return sourceVersion(info)
}

func sourceVersion(info *debug.BuildInfo) string {
	if info.Main.Path == "github.com/AChWorks/achrix" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/AChWorks/achrix" {
			if d.Replace != nil {
				return "development-replacement"
			}
			if d.Version == "" || d.Version == "(devel)" {
				return "development"
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
