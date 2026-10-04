// SPDX-License-Identifier: MPL-2.0

// Package multisite resolves an authorized exact authority to a stable site ID.
// Products own ingress trust, authentication, site bindings and domain isolation.
package multisite

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AChWorks/achrix"
)

const Resolve = "achrix.multisite.resolve"

var (
	ErrConfiguration = errors.New("invalid multisite configuration")
	ErrInput         = errors.New("invalid multisite input")
	ErrUnavailable   = errors.New("multisite unavailable")
	ErrNotFound      = errors.New("multisite site not found")
)

// SiteID is a stable product-selected ASCII identity, not a permission grant.
// Valid identities match [A-Za-z0-9][A-Za-z0-9_-]{0,63}.
type SiteID string

// Site declares exact canonical authorities. Disabled sites retain their
// bindings but resolve like unknown authorities; the default is active.
type Site struct {
	ID          SiteID
	Authorities []string
	Disabled    bool
}

// Config is trusted, finite product composition input. New copies its inventory;
// replacing configuration requires a new validated Module/Application composition.
// No arbitrary site-count limit is imposed; products own inventory/resource budgets.
type Config struct{ Sites []Site }

type binding struct {
	id       SiteID
	disabled bool
}

// Module owns immutable bindings and one-shot lifecycle state. It acquires no
// storage/network resources and starts no workers. Do not share a Module between
// Applications; products explicitly wire its Service to its owning Application.
type Module struct {
	mu       sync.Mutex
	bindings map[string]binding
	state    string
}

// New validates the complete inventory before any runtime effect. Every site
// requires at least one authority; duplicate IDs or authorities (even repeated
// aliases for one site) are rejected. Validation and copying are linear in input.
func New(config Config) (*Module, error) {
	if len(config.Sites) == 0 {
		return nil, ErrConfiguration
	}
	bindings := make(map[string]binding)
	ids := make(map[SiteID]bool, len(config.Sites))
	for _, site := range config.Sites {
		if !validSiteID(site.ID) || ids[site.ID] || len(site.Authorities) == 0 {
			return nil, ErrConfiguration
		}
		ids[site.ID] = true
		for _, authority := range site.Authorities {
			if !validAuthority(authority) {
				return nil, ErrConfiguration
			}
			if _, exists := bindings[authority]; exists {
				return nil, ErrConfiguration
			}
			bindings[authority] = binding{id: site.ID, disabled: site.Disabled}
		}
	}
	return &Module{bindings: bindings, state: "new"}, nil
}

func (m *Module) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{
		ID: "achrix.multisite", Version: achrix.Version(),
		Provides: []achrix.Capability{{ID: Resolve, Version: 1}},
		Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}},
	}
}

func (m *Module) Start(ctx context.Context) error {
	if err := lifecycleContext(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.state != "new" || len(m.bindings) == 0 {
		return ErrUnavailable
	}
	m.state = "ready"
	return nil
}

func (m *Module) Ready(ctx context.Context) error {
	if err := lifecycleContext(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.admission(ctx)
}

// Stop closes Module admission atomically, including with a canceled context.
// There are no acquired resources to drain/clean. Core owns admitted policy
// cancellation/drain; products own ingress and complete domain-operation drain.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.state = "stopped"
	m.mu.Unlock()
	return ctx.Err()
}

func lifecycleContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrConfiguration
	}
	return nil
}

// admission is called with the state lock held. No callback runs under that lock.
func (m *Module) admission(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.state != "ready" {
		return ErrUnavailable
	}
	return nil
}

// Service traverses Core authorization and the owning Module lifecycle.
// app must be the Application composing module; typed collaborators are explicitly
// product-wired, as with other Foundation Services, never discovered from metadata.
type Service struct {
	app    *achrix.Application
	module *Module
}

func NewService(app *achrix.Application, module *Module) (*Service, error) {
	if app == nil || module == nil || len(module.bindings) == 0 {
		return nil, ErrConfiguration
	}
	return &Service{app: app, module: module}, nil
}

// Resolve validates syntax without revealing binding existence, then authorizes
// the exact authority before lookup. A derived maximum one-second context retains
// any earlier caller deadline/cancellation. Policies are trusted synchronous code
// and must honor cancellation; this API cannot forcibly interrupt callbacks.
// Unknown and disabled bindings share ErrNotFound. The result is only a site ID:
// TLS/Host/proxy trust, resource access and site-to-Application/store bindings are
// separate product responsibilities. No normalization, suffix/default-port fallback
// or discovery occurs. Lookup is constant time in configured inventory size.
func (s *Service) Resolve(parent context.Context, actor achrix.Principal, authority string) (SiteID, error) {
	if err := parent.Err(); err != nil {
		return "", err
	}
	if !validAuthority(authority) {
		return "", ErrInput
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	m := s.module
	m.mu.Lock()
	err := m.admission(ctx)
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err = s.app.Authorize(ctx, actor, Resolve, authority); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// This final check and lookup linearize with Stop: a successful resolution
	// belongs before admission closes, never to a stopped Module.
	if err = m.admission(ctx); err != nil {
		return "", err
	}
	b, found := m.bindings[authority]
	if !found || b.disabled {
		return "", ErrNotFound
	}
	return b.id, nil
}

func validSiteID(id SiteID) bool {
	if len(id) == 0 || len(id) > 64 || !asciiAlphanumeric(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if !asciiAlphanumeric(id[i]) && id[i] != '_' && id[i] != '-' {
			return false
		}
	}
	return true
}

func asciiAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func validAuthority(authority string) bool {
	// DNS name <=253 bytes, colon plus at most five port digits. IPv6 is smaller.
	// Bound work before parsing/policy even when runtime input is arbitrarily large.
	if len(authority) == 0 || len(authority) > 259 {
		return false
	}
	var host, port string
	if authority[0] == '[' {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return false
		}
		host = authority[1:end]
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" || address.String() != host {
			return false
		}
		if end+1 == len(authority) {
			return true
		}
		if authority[end+1] != ':' {
			return false
		}
		port = authority[end+2:]
	} else {
		host = authority
		if before, after, found := strings.Cut(authority, ":"); found {
			host, port = before, after
			if !validPort(port) {
				return false
			}
		}
		address, err := netip.ParseAddr(host)
		if err == nil {
			return address.Is4() && address.String() == host
		}
		if !validDNS(host) {
			return false
		}
		return true
	}
	return validPort(port)
}

func validPort(port string) bool {
	if len(port) == 0 || len(port) > 5 || port[0] == '0' {
		return false
	}
	for i := range port {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n <= 65535
}

func validDNS(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	// Reject legacy hexadecimal IPv4 spellings instead of accepting them as DNS.
	// Numeric-only/octal/short forms are rejected by the final-label rule below.
	legacyNumbers := true
	for _, label := range labels {
		part := label
		hex := strings.HasPrefix(part, "0x")
		if hex {
			part = part[2:]
		}
		if part == "" {
			legacyNumbers = false
		}
		for i := range part {
			c := part[i]
			if !(c >= '0' && c <= '9' || hex && c >= 'a' && c <= 'f') {
				legacyNumbers = false
			}
		}
	}
	if legacyNumbers {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range label {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	// The final DNS label must contain a letter, distinguishing DNS names from
	// numeric/short/legacy IPv4 spellings without interpreting or normalizing them.
	for i := range labels[len(labels)-1] {
		if c := labels[len(labels)-1][i]; c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
}
