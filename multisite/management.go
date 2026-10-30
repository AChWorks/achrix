// SPDX-License-Identifier: MPL-2.0

package multisite

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/AChWorks/achrix"
)

const (
	InventoryRead  = "achrix.multisite.inventory.read"
	PlanActivation = "achrix.multisite.activation.plan"
	// InventoryTarget gates collection discovery in addition to exact-site reads.
	InventoryTarget = ""
)

var ErrConflict = errors.New("multisite configuration conflict")

// SiteSelection selects compiled Modules for one site's next Application.
// Omission or removal retains the owning Module's durable data.
type SiteSelection struct {
	ID      SiteID
	Modules []string
	// Generation is a product-persisted monotonic selection precondition.
	// Replacement increments it; disabling/re-enabling must retain it.
	Generation uint64
}

// ManagementConfig is trusted composition input. Available contains public,
// side-effect-free descriptors for product-wired factories, never service handles.
// Sites may omit a declared site to select no Modules for that site.
type ManagementConfig struct {
	Available []achrix.Descriptor
	Sites     []SiteSelection
}

// SiteInfo exposes only authorized public site/selection metadata. Disabled
// retains its resolver meaning; it is not a universal product lifecycle state.
// Revision binds this site's selection, Disabled value and available catalogue.
type SiteInfo struct {
	ID       SiteID
	Disabled bool
	Modules  []string
	Revision string
}

type InventoryPage struct {
	Sites      []SiteInfo
	NextCursor string
}

type ActivationRequest struct {
	SiteID           SiteID
	ExpectedRevision string
	Modules          []string
}

// ActivationPlan is an immutable target-only proposal. It contains no authority,
// service/store handle, other-site inventory or private product configuration.
type ActivationPlan struct {
	site          SiteID
	before, after string
	modules       []string
	descriptors   []achrix.Descriptor
	generation    uint64
}

func (p ActivationPlan) Site() SiteID           { return p.site }
func (p ActivationPlan) BeforeRevision() string { return p.before }
func (p ActivationPlan) AfterRevision() string  { return p.after }
func (p ActivationPlan) Selection() SiteSelection {
	return SiteSelection{ID: p.site, Modules: slices.Clone(p.modules), Generation: p.generation}
}
func (p ActivationPlan) Descriptors() []achrix.Descriptor {
	return cloneDescriptors(p.descriptors)
}

type management struct {
	catalog         map[string]achrix.Descriptor
	catalogRevision string
	sites           map[SiteID]SiteInfo
	ids             []SiteID
	generations     map[SiteID]uint64
}

var moduleIdentity = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

// NewManaged adds optional inventory and selection planning to the existing
// resolver. It validates and copies all input before effects, constructs no
// selected Module and acquires no resource. Existing New remains resolver-only.
func NewManaged(config Config, input ManagementConfig) (*Module, error) {
	module, err := New(config)
	if err != nil {
		return nil, err
	}
	managed, err := newManagement(config, input)
	if err != nil {
		return nil, err
	}
	module.management = managed
	return module, nil
}

func newManagement(config Config, input ManagementConfig) (*management, error) {
	// Catalogue/request bounds protect planning and public output. There is no
	// global site-count ceiling; products own their finite inventory budget.
	if len(input.Available) > 256 {
		return nil, fmt.Errorf("%w: available: at most 256 module descriptors are supported", ErrConfiguration)
	}
	if len(input.Sites) > len(config.Sites) {
		return nil, fmt.Errorf("%w: selections: count exceeds declared sites", ErrConfiguration)
	}
	m := &management{catalog: make(map[string]achrix.Descriptor, len(input.Available)), sites: make(map[SiteID]SiteInfo, len(config.Sites)), generations: make(map[SiteID]uint64, len(config.Sites))}
	catalog := cloneDescriptors(input.Available)
	for i, descriptor := range catalog {
		if err := validateDescriptor(descriptor); err != nil {
			return nil, fmt.Errorf("available[%d]: %w", i, err)
		}
		if _, duplicate := m.catalog[descriptor.ID]; duplicate {
			return nil, fmt.Errorf("%w: available[%d].id: duplicate module %s", ErrConfiguration, i, descriptor.ID)
		}
		m.catalog[descriptor.ID] = descriptor
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].ID < catalog[j].ID })
	m.catalogRevision = digest(catalog)
	for _, site := range config.Sites {
		m.sites[site.ID] = SiteInfo{ID: site.ID, Disabled: site.Disabled}
		m.ids = append(m.ids, site.ID)
	}
	sort.Slice(m.ids, func(i, j int) bool { return m.ids[i] < m.ids[j] })
	seen := make(map[SiteID]bool, len(input.Sites))
	for i, selection := range input.Sites {
		site, exists := m.sites[selection.ID]
		if !exists {
			return nil, fmt.Errorf("%w: selections[%d].id: site is not declared in the supplied configuration", ErrConfiguration, i)
		}
		if seen[selection.ID] {
			return nil, fmt.Errorf("%w: selections[%d].id: duplicate selection", ErrConfiguration, i)
		}
		seen[selection.ID] = true
		modules, _, err := selectedDescriptors(m.catalog, selection.Modules)
		if err != nil {
			return nil, fmt.Errorf("%w: selections[%d].modules: %w", ErrConfiguration, i, err)
		}
		site.Modules = modules
		m.generations[selection.ID] = selection.Generation
		m.sites[selection.ID] = site
	}
	for id, site := range m.sites {
		site.Revision = selectionRevision(site.ID, site.Disabled, site.Modules, m.catalogRevision, m.generations[id])
		m.sites[id] = site
	}
	return m, nil
}

func validModuleIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 128 && moduleIdentity.MatchString(value)
}

func validateDescriptor(d achrix.Descriptor) error {
	if !validModuleIdentity(d.ID) {
		return fmt.Errorf("%w: id: use a bounded lowercase module identifier", ErrConfiguration)
	}
	if d.Version == "" || len(d.Version) > 256 {
		return fmt.Errorf("%w: module %s version: provide an implementation version of 1..256 bytes", ErrConfiguration, d.ID)
	}
	if len(d.Provides) > 256 || len(d.Requires) > 256 || len(d.Optional) > 256 {
		return fmt.Errorf("%w: module %s capabilities: each group is limited to 256 declarations", ErrConfiguration, d.ID)
	}
	provided := make(map[string]bool, len(d.Provides))
	for i, c := range d.Provides {
		if !validModuleIdentity(c.ID) || c.Version == 0 {
			return fmt.Errorf("%w: module %s provides[%d]: valid ID and positive ABI revision required", ErrConfiguration, d.ID, i)
		}
		if c.ID == "achrix.authorization" {
			return fmt.Errorf("%w: module %s provides[%d]: authorization is owned by Core", ErrConfiguration, d.ID, i)
		}
		if provided[c.ID] {
			return fmt.Errorf("%w: module %s provides[%d]: duplicate capability", ErrConfiguration, d.ID, i)
		}
		provided[c.ID] = true
	}
	seen := make(map[string]bool, len(d.Requires)+len(d.Optional))
	for _, group := range []struct {
		name         string
		capabilities []achrix.Capability
	}{{"requires", d.Requires}, {"optional", d.Optional}} {
		for i, c := range group.capabilities {
			if !validModuleIdentity(c.ID) || c.Version == 0 {
				return fmt.Errorf("%w: module %s %s[%d]: valid ID and positive ABI revision required", ErrConfiguration, d.ID, group.name, i)
			}
			if seen[c.ID] {
				return fmt.Errorf("%w: module %s %s[%d]: duplicate dependency", ErrConfiguration, d.ID, group.name, i)
			}
			seen[c.ID] = true
		}
	}
	return nil
}

// descriptorOnly cannot construct/start an actual Module or acquire resources.
// Core validates the same dependency ABI, duplicate provider and cycle rules
// that will govern the product's reconstructed Application.
type descriptorOnly struct{ descriptor achrix.Descriptor }

func (m descriptorOnly) Descriptor() achrix.Descriptor { return m.descriptor }
func (descriptorOnly) Start(context.Context) error     { return nil }
func (descriptorOnly) Ready(context.Context) error     { return nil }
func (descriptorOnly) Stop(context.Context) error      { return nil }

func selectedDescriptors(catalog map[string]achrix.Descriptor, requested []string) ([]string, []achrix.Descriptor, error) {
	if len(requested) > 256 {
		return nil, nil, fmt.Errorf("%w: modules: select at most 256 modules", ErrInput)
	}
	for i, id := range requested {
		if !validModuleIdentity(id) {
			return nil, nil, fmt.Errorf("%w: modules[%d]: invalid module identifier", ErrInput, i)
		}
	}
	ids := append([]string(nil), requested...)
	sort.Strings(ids)
	descriptors := make([]achrix.Descriptor, 0, len(ids))
	modules := make([]achrix.Module, 0, len(ids))
	for i, id := range ids {
		if i > 0 && ids[i-1] == id {
			return nil, nil, fmt.Errorf("%w: modules: duplicate selection %s", ErrInput, id)
		}
		descriptor, exists := catalog[id]
		if !exists {
			return nil, nil, fmt.Errorf("%w: modules: %s is absent from the available catalog", ErrInput, id)
		}
		descriptors = append(descriptors, cloneDescriptor(descriptor))
		modules = append(modules, descriptorOnly{descriptor})
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second},
		achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return achrix.ErrDenied }), modules...)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: selected module graph: %w", ErrConfiguration, err)
	}
	// New has acquired no Module resources; close its unused Core admission state.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		return nil, nil, fmt.Errorf("%w: close planning composition: %w", ErrUnavailable, err)
	}
	return ids, descriptors, nil
}

func digest(value any) string {
	data, _ := json.Marshal(value) // Only known concrete acyclic public metadata.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func selectionRevision(id SiteID, disabled bool, modules []string, catalogue string, generation uint64) string {
	return digest(struct {
		Site       SiteID
		Disabled   bool
		Modules    []string
		Catalogue  string
		Generation uint64
	}{id, disabled, modules, catalogue, generation})
}
func cloneDescriptor(d achrix.Descriptor) achrix.Descriptor {
	d.Provides = slices.Clone(d.Provides)
	d.Requires = slices.Clone(d.Requires)
	d.Optional = slices.Clone(d.Optional)
	return d
}
func cloneDescriptors(input []achrix.Descriptor) []achrix.Descriptor {
	out := make([]achrix.Descriptor, len(input))
	for i, d := range input {
		out[i] = cloneDescriptor(d)
	}
	return out
}
func cloneManagement(input ManagementConfig) ManagementConfig {
	output := ManagementConfig{Available: cloneDescriptors(input.Available), Sites: make([]SiteSelection, len(input.Sites))}
	for i, s := range input.Sites {
		output.Sites[i] = SiteSelection{ID: s.ID, Modules: slices.Clone(s.Modules), Generation: s.Generation}
	}
	return output
}
func cloneInfo(info SiteInfo) SiteInfo { info.Modules = slices.Clone(info.Modules); return info }

func (s *Service) managedAdmission(ctx context.Context) (*management, error) {
	s.module.mu.Lock()
	defer s.module.mu.Unlock()
	if err := s.module.admission(ctx); err != nil {
		return nil, err
	}
	if s.module.management == nil {
		return nil, ErrUnavailable
	}
	return s.module.management, nil
}

// Site authorizes the exact site before revealing whether it is configured.
func (s *Service) Site(parent context.Context, actor achrix.Principal, id SiteID) (SiteInfo, error) {
	if err := parent.Err(); err != nil {
		return SiteInfo{}, err
	}
	if !validSiteID(id) {
		return SiteInfo{}, fmt.Errorf("%w: site_id: use the bounded canonical site identifier", ErrInput)
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	m, err := s.managedAdmission(ctx)
	if err != nil {
		return SiteInfo{}, err
	}
	if err = s.app.Authorize(ctx, actor, InventoryRead, string(id)); err != nil {
		return SiteInfo{}, err
	}
	s.module.mu.Lock()
	defer s.module.mu.Unlock()
	if err = s.module.admission(ctx); err != nil {
		return SiteInfo{}, err
	}
	site, exists := m.sites[id]
	if !exists {
		return SiteInfo{}, ErrNotFound
	}
	return cloneInfo(site), nil
}

// Inventory requires both collection-discovery permission and exact-site reads.
// Denied sites produce no entry, total or cursor identity. The cursor contains
// only the last returned authorized SiteID. Evaluation failure aborts the page.
// Work cooperatively stops within one second; result size is 1..100 sites.
func (s *Service) Inventory(parent context.Context, actor achrix.Principal, cursor string, limit int) (InventoryPage, error) {
	if err := parent.Err(); err != nil {
		return InventoryPage{}, err
	}
	after, err := decodeInventoryCursor(cursor)
	if err != nil {
		return InventoryPage{}, fmt.Errorf("%w: cursor: use a previously returned inventory cursor", ErrInput)
	}
	if limit < 1 || limit > 100 {
		return InventoryPage{}, fmt.Errorf("%w: limit: choose 1..100 entries", ErrInput)
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	m, err := s.managedAdmission(ctx)
	if err != nil {
		return InventoryPage{}, err
	}
	if err = s.app.Authorize(ctx, actor, InventoryRead, InventoryTarget); err != nil {
		return InventoryPage{}, err
	}
	start := sort.Search(len(m.ids), func(i int) bool { return m.ids[i] > after })
	page := InventoryPage{Sites: make([]SiteInfo, 0, limit)}
	for _, id := range m.ids[start:] {
		if err = ctx.Err(); err != nil {
			return InventoryPage{}, err
		}
		err = s.app.Authorize(ctx, actor, InventoryRead, string(id))
		if errors.Is(err, achrix.ErrDenied) {
			continue
		}
		if err != nil {
			return InventoryPage{}, err
		}
		if len(page.Sites) == limit {
			page.NextCursor = encodeInventoryCursor(page.Sites[len(page.Sites)-1].ID)
			break
		}
		page.Sites = append(page.Sites, cloneInfo(m.sites[id]))
	}
	s.module.mu.Lock()
	defer s.module.mu.Unlock()
	if err = s.module.admission(ctx); err != nil {
		return InventoryPage{}, err
	}
	return page, nil
}
func encodeInventoryCursor(id SiteID) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
func decodeInventoryCursor(cursor string) (SiteID, error) {
	if cursor == "" {
		return "", nil
	}
	if len(cursor) > 86 {
		return "", ErrInput
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	id := SiteID(raw)
	if err != nil || !validSiteID(id) || encodeInventoryCursor(id) != cursor {
		return "", ErrInput
	}
	return id, nil
}

// PlanActivation authorizes a target-specific proposal, not activation itself.
// The returned plan exposes only the target's public selection. Persist/activate
// only after fresh authorization and a product-owned compare-and-swap, followed
// by supported domain drain/reconstruction/readiness/activation.
func (s *Service) PlanActivation(parent context.Context, actor achrix.Principal, request ActivationRequest) (ActivationPlan, error) {
	if err := parent.Err(); err != nil {
		return ActivationPlan{}, err
	}
	if !validSiteID(request.SiteID) {
		return ActivationPlan{}, fmt.Errorf("%w: site_id: use a canonical site identifier", ErrInput)
	}
	if len(request.ExpectedRevision) != 64 {
		return ActivationPlan{}, fmt.Errorf("%w: expected_revision: provide the current 64-character revision", ErrInput)
	}
	if len(request.Modules) > 256 {
		return ActivationPlan{}, fmt.Errorf("%w: modules: select at most 256 modules", ErrInput)
	}
	if _, err := hex.DecodeString(request.ExpectedRevision); err != nil {
		return ActivationPlan{}, fmt.Errorf("%w: expected_revision: use a hexadecimal revision", ErrInput)
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	m, err := s.managedAdmission(ctx)
	if err != nil {
		return ActivationPlan{}, err
	}
	if err = s.app.Authorize(ctx, actor, PlanActivation, string(request.SiteID)); err != nil {
		return ActivationPlan{}, err
	}
	site, exists := m.sites[request.SiteID]
	if !exists || site.Disabled {
		return ActivationPlan{}, ErrNotFound
	}
	if request.ExpectedRevision != site.Revision {
		return ActivationPlan{}, fmt.Errorf("%w: expected_revision: refresh the authorized site before planning again", ErrConflict)
	}
	if m.generations[site.ID] == math.MaxUint64 {
		return ActivationPlan{}, fmt.Errorf("%w: generation exhausted; operator reconciliation required", ErrConflict)
	}
	ids, descriptors, err := selectedDescriptors(m.catalog, request.Modules)
	if err != nil {
		return ActivationPlan{}, err
	}
	s.module.mu.Lock()
	defer s.module.mu.Unlock()
	if err = s.module.admission(ctx); err != nil {
		return ActivationPlan{}, err
	}
	return ActivationPlan{site: site.ID, before: site.Revision, after: selectionRevision(site.ID, site.Disabled, ids, m.catalogRevision, m.generations[site.ID]+1), modules: ids, descriptors: descriptors, generation: m.generations[site.ID] + 1}, nil
}

// Replacement is a pure trusted-caller helper over the caller's OWN supplied
// configuration. It never retrieves another site's private server configuration.
// It validates the expected target/catalogue revision and returns a defensive
// updated copy. It acquires no resource and grants no permission. Before actual
// persistence/activation the product reauthorizes and serializes/CASes its state.
func (p ActivationPlan) Replacement(current Config, input ManagementConfig) (ManagementConfig, error) {
	if !validSiteID(p.site) || p.before == "" || p.after == "" {
		return ManagementConfig{}, fmt.Errorf("%w: plan: obtain a valid authorized activation plan", ErrInput)
	}
	module, err := NewManaged(current, input)
	if err != nil {
		return ManagementConfig{}, err
	}
	site, exists := module.management.sites[p.site]
	if !exists || site.Disabled {
		return ManagementConfig{}, ErrNotFound
	}
	if site.Revision != p.before || module.management.generations[site.ID] == math.MaxUint64 || module.management.generations[site.ID]+1 != p.generation {
		return ManagementConfig{}, fmt.Errorf("%w: selection revision or generation changed; refresh and reauthorize before replacement", ErrConflict)
	}
	ids, _, err := selectedDescriptors(module.management.catalog, p.modules)
	if err != nil {
		return ManagementConfig{}, err
	}
	if selectionRevision(site.ID, site.Disabled, ids, module.management.catalogRevision, p.generation) != p.after {
		return ManagementConfig{}, fmt.Errorf("%w: planned selection no longer matches its after revision", ErrConflict)
	}
	output := cloneManagement(input)
	found := false
	for i := range output.Sites {
		if output.Sites[i].ID == p.site {
			output.Sites[i].Modules = ids
			output.Sites[i].Generation = p.generation
			found = true
			break
		}
	}
	if !found {
		output.Sites = append(output.Sites, SiteSelection{ID: p.site, Modules: ids, Generation: p.generation})
	}
	return output, nil
}
