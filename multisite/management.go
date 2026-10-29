// SPDX-License-Identifier: MPL-2.0

package multisite

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if len(input.Available) > 256 || len(input.Sites) > len(config.Sites) {
		return nil, ErrConfiguration
	}
	m := &management{catalog: make(map[string]achrix.Descriptor, len(input.Available)), sites: make(map[SiteID]SiteInfo, len(config.Sites)), generations: make(map[SiteID]uint64, len(config.Sites))}
	catalog := cloneDescriptors(input.Available)
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].ID < catalog[j].ID })
	for _, descriptor := range catalog {
		if !validDescriptor(descriptor) {
			return nil, ErrConfiguration
		}
		if _, duplicate := m.catalog[descriptor.ID]; duplicate {
			return nil, ErrConfiguration
		}
		m.catalog[descriptor.ID] = descriptor
	}
	m.catalogRevision = digest(catalog)
	for _, site := range config.Sites {
		m.sites[site.ID] = SiteInfo{ID: site.ID, Disabled: site.Disabled}
		m.ids = append(m.ids, site.ID)
	}
	sort.Slice(m.ids, func(i, j int) bool { return m.ids[i] < m.ids[j] })
	seen := make(map[SiteID]bool, len(input.Sites))
	for _, selection := range input.Sites {
		site, exists := m.sites[selection.ID]
		if !exists || seen[selection.ID] {
			return nil, ErrConfiguration
		}
		seen[selection.ID] = true
		modules, _, err := selectedDescriptors(m.catalog, selection.Modules)
		if err != nil {
			return nil, ErrConfiguration
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

func validDescriptor(d achrix.Descriptor) bool {
	if !validModuleIdentity(d.ID) || d.Version == "" || len(d.Version) > 256 ||
		len(d.Provides) > 256 || len(d.Requires) > 256 || len(d.Optional) > 256 {
		return false
	}
	provided := make(map[string]bool, len(d.Provides))
	for _, c := range d.Provides {
		if !validModuleIdentity(c.ID) || c.ID == "achrix.authorization" || c.Version == 0 || provided[c.ID] {
			return false
		}
		provided[c.ID] = true
	}
	seen := make(map[string]bool, len(d.Requires)+len(d.Optional))
	for _, group := range [][]achrix.Capability{d.Requires, d.Optional} {
		for _, c := range group {
			if !validModuleIdentity(c.ID) || c.Version == 0 || seen[c.ID] {
				return false
			}
			seen[c.ID] = true
		}
	}
	return true
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
		return nil, nil, ErrInput
	}
	ids := append([]string(nil), requested...)
	sort.Strings(ids)
	descriptors := make([]achrix.Descriptor, 0, len(ids))
	modules := make([]achrix.Module, 0, len(ids))
	for i, id := range ids {
		if !validModuleIdentity(id) || i > 0 && ids[i-1] == id {
			return nil, nil, ErrInput
		}
		descriptor, exists := catalog[id]
		if !exists {
			return nil, nil, ErrInput
		}
		descriptors = append(descriptors, cloneDescriptor(descriptor))
		modules = append(modules, descriptorOnly{descriptor})
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second},
		achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return achrix.ErrDenied }), modules...)
	if err != nil {
		return nil, nil, ErrConfiguration
	}
	// New has acquired no Module resources; close its unused Core admission state.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		return nil, nil, ErrUnavailable
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
		return SiteInfo{}, ErrInput
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
	if err != nil || limit < 1 || limit > 100 {
		return InventoryPage{}, ErrInput
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
	page := InventoryPage{Sites: make([]SiteInfo, 0, limit)}
	for _, id := range m.ids {
		if err = ctx.Err(); err != nil {
			return InventoryPage{}, err
		}
		if id <= after {
			continue
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
	if !validSiteID(request.SiteID) || len(request.ExpectedRevision) != 64 || len(request.Modules) > 256 {
		return ActivationPlan{}, ErrInput
	}
	if _, err := hex.DecodeString(request.ExpectedRevision); err != nil {
		return ActivationPlan{}, ErrInput
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
	if request.ExpectedRevision != site.Revision || m.generations[site.ID] == math.MaxUint64 {
		return ActivationPlan{}, ErrConflict
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
		return ManagementConfig{}, ErrInput
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
		return ManagementConfig{}, ErrConflict
	}
	ids, _, err := selectedDescriptors(module.management.catalog, p.modules)
	if err != nil {
		return ManagementConfig{}, err
	}
	if selectionRevision(site.ID, site.Disabled, ids, module.management.catalogRevision, p.generation) != p.after {
		return ManagementConfig{}, ErrConflict
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
