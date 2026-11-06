// SPDX-License-Identifier: MPL-2.0
// Package oauth provides an experimental, optional in-process authorization
// server. This initial protocol/API profile is not approved for production or
// release. A product owns its users, sessions, policy, TLS and configuration.
package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConfiguration = errors.New("oauth configuration invalid")
	ErrInvalidGrant  = errors.New("oauth grant invalid")
	ErrUnavailable   = errors.New("oauth state unavailable")
)

// Client is registered by trusted product configuration, not by a user request.
// A public client ID may be an operator-pre-enrolled canonical HTTPS metadata
// URL. This profile never fetches arbitrary request-supplied metadata.
// SecretDigest is SHA-256 of a randomly generated >= 32-byte client secret,
// provisioned out-of-band. Never configure a human-memorable secret.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	Scopes       []string
	Confidential bool
	SecretDigest [32]byte
	Enabled      bool
}

// Session is supplied by trusted product authentication only. Binding MUST be
// the SHA-256 digest of the current opaque browser session credential (not user
// ID). Identity.Web plus identity.CookieName can implement this without importing
// Identity into the issuer. A rotated/logged-out session cannot resume consent.
type Session struct {
	Subject string
	Binding [32]byte
}

// Grant represents independently revocable, resource-bound user delegation.
// Scopes are a ceiling on transport authority; never product business rights.
type Grant struct {
	ID, Subject, ClientID, Issuer, Resource string
	Scopes                                  []string
	CreatedAt                               time.Time
}

// ConsentView contains trusted product presentation inputs and opaque,
// short-lived one-use transaction values. Never log or persist Transaction
// or CSRF outside the issuer's protected in-flight consent response.
// The product must render an escaped, accessible same-origin POST form to
// FormAction with transaction, csrf and decision=allow|deny fields.
type ConsentView struct {
	Subject     string
	ClientID    string
	ClientName  string
	RedirectURI string
	Resource    string
	Scopes      []string
	FormAction  string
	Transaction string
	CSRF        string
	ExpiresAt   time.Time
}

// Config contains no global mutable state. The caller owns DB initialization,
// TLS, ingress, the consent UI and protected secrets. Callbacks must be
// safe for concurrent use.
type Config struct {
	Issuer   string
	Resource string
	Clients  []Client
	// ClientLookup must read currently-authoritative client state on every call.
	// Clients is only the product-approved registration ceiling; live state may
	// reduce but never silently expand this initial pre-enrolled profile.
	ClientLookup func(context.Context, string) (Client, error)
	Pool         *pgxpool.Pool
	Session      func(context.Context, *http.Request) (Session, error)
	// Permit checks current product rights for this subject/client/resource/scope.
	// Denial or unavailability must fail closed; HTTP does not expose its cause.
	Permit func(context.Context, Grant) error
	// RenderConsent is required, product-owned, and runs only after a
	// valid authenticated session/permission and fresh one-use consent
	// transaction exist. It must render on the issuer's same HTTPS origin.
	// No default Foundation-hosted login or HTML UX is provided.
	RenderConsent func(http.ResponseWriter, *http.Request, ConsentView)
	// Diagnostic must preserve protected errors and correlation elsewhere.
	Diagnostic func(context.Context, string, string, error)
	Now        func() time.Time
}

// Server is opt-in; construction has no network, DB connection or timer effects.
type Server struct {
	config  Config
	clients map[string]Client
	issuer  *url.URL
	path    string
	origin  string
	slots   chan struct{}
	sdk     *sdkEngine
}

func New(config Config) (*Server, error) {
	u, err := url.Parse(config.Issuer)
	if err != nil || len(config.Issuer) > 2048 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.String() != config.Issuer || !validHTTPHost(u) || !validURLPath(u, false) {
		return nil, fmt.Errorf("%w: canonical HTTPS issuer required", ErrConfiguration)
	}
	resource, err := url.Parse(config.Resource)
	if err != nil || len(config.Resource) > 2048 || resource.Scheme != "https" || resource.Host == "" || resource.User != nil || resource.Fragment != "" || resource.RawQuery != "" || resource.Opaque != "" || resource.String() != config.Resource || !validHTTPHost(resource) || !validURLPath(resource, true) {
		return nil, fmt.Errorf("%w: canonical HTTPS resource required", ErrConfiguration)
	}
	if config.Pool == nil || config.ClientLookup == nil || config.Session == nil || config.Permit == nil || config.RenderConsent == nil || config.Diagnostic == nil || len(config.Clients) == 0 || len(config.Clients) > 128 {
		return nil, fmt.Errorf("%w: pool, current client lookup, session, policy, consent presentation, diagnostic and 1..128 clients required", ErrConfiguration)
	}
	clients := make(map[string]Client, len(config.Clients))
	for _, c := range config.Clients {
		if !validClientID(c.ID) || len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > 12 || len(c.Scopes) == 0 || len(c.Scopes) > 16 || len(c.Name) > 120 {
			return nil, fmt.Errorf("%w: invalid client metadata", ErrConfiguration)
		}
		if _, exists := clients[c.ID]; exists {
			return nil, fmt.Errorf("%w: duplicated client", ErrConfiguration)
		}
		if c.Confidential && c.SecretDigest == ([32]byte{}) || !c.Confidential && c.SecretDigest != ([32]byte{}) ||
			strings.HasPrefix(c.ID, "https://") && c.Confidential {
			// The enrolled URL-client subset supports only the public
			// "none" method; private_key_jwt is not implemented here.
			return nil, fmt.Errorf("%w: invalid client authentication", ErrConfiguration)
		}
		for _, redirect := range c.RedirectURIs {
			ru, e := url.Parse(redirect)
			if e != nil || len(redirect) > 2048 || ru.Scheme != "https" || ru.Host == "" || ru.User != nil || ru.Fragment != "" || ru.Opaque != "" || ru.String() != redirect || !validHTTPHost(ru) || !validURLPath(ru, true) {
				return nil, fmt.Errorf("%w: registered redirect must be exact HTTPS", ErrConfiguration)
			}
			query, e := url.ParseQuery(ru.RawQuery)
			if e != nil || query.Has("code") || query.Has("iss") || query.Has("state") || query.Has("error") || query.Has("error_description") {
				return nil, fmt.Errorf("%w: callback query conflicts with protocol", ErrConfiguration)
			}
		}
		if slices.Contains(c.Scopes, "") || slices.Contains(c.Scopes, "openid") {
			return nil, fmt.Errorf("%w: empty scope", ErrConfiguration)
		}
		seen := map[string]bool{}
		for _, scope := range c.Scopes {
			if seen[scope] || !validScope(scope) {
				return nil, fmt.Errorf("%w: invalid or repeated scope", ErrConfiguration)
			}
			seen[scope] = true
		}
		c.RedirectURIs = append([]string(nil), c.RedirectURIs...)
		c.Scopes = append([]string(nil), c.Scopes...)
		clients[c.ID] = c
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	s := &Server{config: config, clients: clients, issuer: u, path: u.Path, origin: u.Scheme + "://" + u.Host, slots: make(chan struct{}, 16)}
	s.sdk = newSDKEngine(s)
	return s, nil
}

// SecretDigest is suitable only for a high-entropy, product-provisioned random
// client secret. It is deliberately not a password hashing function.
func SecretDigest(randomSecret string) [32]byte { return sha256.Sum256([]byte(randomSecret)) }

// client consults the current product registry, bounded by reviewed enrollment.
func (s *Server) client(ctx context.Context, id string) (Client, error) {
	ceiling, enrolled := s.clients[id]
	if !enrolled || !ceiling.Enabled {
		return Client{}, ErrInvalidGrant
	}
	current, err := s.config.ClientLookup(ctx, id)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			return Client{}, ErrInvalidGrant
		}
		return Client{}, fmt.Errorf("%w: client lookup: %v", ErrUnavailable, err)
	}
	if !current.Enabled || current.ID != id || current.Confidential != ceiling.Confidential {
		return Client{}, ErrInvalidGrant
	}
	if len(current.RedirectURIs) == 0 || len(current.Scopes) == 0 || len(current.RedirectURIs) > len(ceiling.RedirectURIs) || len(current.Scopes) > len(ceiling.Scopes) {
		return Client{}, ErrInvalidGrant
	}
	for _, uri := range current.RedirectURIs {
		if !slices.Contains(ceiling.RedirectURIs, uri) {
			return Client{}, ErrInvalidGrant
		}
	}
	for _, scope := range current.Scopes {
		if !slices.Contains(ceiling.Scopes, scope) || scope == "openid" {
			return Client{}, ErrInvalidGrant
		}
	}
	if current.Confidential && current.SecretDigest == ([32]byte{}) {
		return Client{}, ErrInvalidGrant
	}
	current.RedirectURIs = slices.Clone(current.RedirectURIs)
	current.Scopes = slices.Clone(current.Scopes)
	return current, nil
}

// validURLPath forbids dot segments, doubled separators and encoded paths
// that a mux, proxy or resource server could canonicalize differently.
func validURLPath(u *url.URL, allowTrailing bool) bool {
	if u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") || strings.Contains(u.Path, "//") {
		return false
	}
	if u.Path == "" {
		return true
	}
	cleaned := path.Clean(u.Path)
	if u.Path == cleaned {
		return true
	}
	return allowTrailing && u.Path == cleaned+"/"
}

// validHTTPHost rejects wildcard, ambiguous, noncanonical and invalid DNS
// or port syntax. HTTPS still remains required by each caller.
func validHTTPHost(u *url.URL) bool {
	host := u.Hostname()
	if host == "" || strings.HasSuffix(host, ".") || len(host) > 253 {
		return false
	}
	if port := u.Port(); port != "" {
		num, err := strconv.Atoi(port)
		if err != nil || num < 1 || num > 65535 || strconv.Itoa(num) != port {
			return false
		}
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			letter := b >= 'a' && b <= 'z'
			number := b >= '0' && b <= '9'
			if !letter && !number && !(b == '-' && i > 0 && i < len(label)-1) {
				return false
			}
		}
	}
	return true
}

// validClientID accepts small owner-enrolled opaque IDs or canonical HTTPS
// metadata document identifiers. The HTTPS form is a *pre-enrolled* public
// client, not arbitrary URL-based dynamic registration: no request causes a
// metadata fetch or enables an unknown client. A product must independently
// verify the metadata document and pin its exact allowed callback(s) before
// creating the Server (CIMD Internet-Draft, pre-registration mode).
func validClientID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	if strings.HasPrefix(id, "https://") {
		u, err := url.Parse(id)
		if err != nil || u.String() != id || u.Scheme != "https" ||
			u.Host == "" || u.Port() != "" || u.User != nil ||
			u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
			u.Path == "" || u.Path == "/" || !validURLPath(u, false) ||
			!validHTTPHost(u) || net.ParseIP(u.Hostname()) != nil ||
			!strings.Contains(u.Hostname(), ".") ||
			strings.HasSuffix(u.Hostname(), ".localhost") ||
			strings.HasSuffix(u.Hostname(), ".local") ||
			strings.HasSuffix(u.Hostname(), ".internal") {
			return false
		}
		return true
	}
	if len(id) > 200 {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~') {
			return false
		}
	}
	return true
}

// RFC 6749 scope-token is only printable ASCII excluding quote and backslash.
// Restrict to 1..80 bytes to avoid ambiguous Unicode or delimiter identities.
func validScope(scope string) bool {
	if len(scope) == 0 || len(scope) > 80 {
		return false
	}
	for i := 0; i < len(scope); i++ {
		b := scope[i]
		if b < 0x21 || b > 0x7e || b == 0x22 || b == 0x5c {
			return false
		}
	}
	return true
}
