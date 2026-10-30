// SPDX-License-Identifier: MPL-2.0

// Package bridge adapts deliberately registered typed application operations to
// bounded, authenticated MCP and JSON HTTP. Products supply credential
// verification and current admission; their application services still own
// domain authorization. Bridge does not issue credentials or implement OAuth
// login, consent, refresh, revocation storage, or account linking.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AChWorks/achrix"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// ErrInvalidCredential marks credentials that are invalid, expired, revoked,
// or issued for another resource. Other verifier errors mean verification is
// unavailable. Neither error's original text is returned to the client.
var ErrInvalidCredential = errors.New("invalid bridge credential")

// The SDK reads this compatibility switch during package initialization too.
// Its legacy behavior retains sessions even when Stateless is requested.
var sessionCompatibility = func() bool {
	value := ""
	for part := range strings.SplitSeq(os.Getenv("MCPGODEBUG"), ",") {
		key, v, _ := strings.Cut(part, "=")
		if strings.TrimSpace(key) == "allowsessionsinstateless" {
			value = strings.TrimSpace(v)
		}
	}
	return value == "1"
}()

// Caller is verified product identity. None of these fields comes from client
// JSON, MCP clientInfo, forwarded headers, or a claimed actor.
type Caller struct {
	Principal    achrix.Principal
	ClientID     string
	ConnectionID string
}

// Credential is the result of product-supplied verification. Scopes narrow
// access to this transport; they do not replace live application authorization.
type Credential struct {
	Caller    Caller
	ExpiresAt time.Time
	Scopes    []string
}

// VerifyToken must authenticate the issuer and intended resource/audience,
// check expiry and current revocation, and bind the user and client. The
// callback is concurrent, context-bounded, and must not log its bearer argument.
type VerifyToken func(context.Context, string) (Credential, error)

// Limits bounds active work. Zero values select 64 KiB input, 1 MiB response,
// 16 concurrent requests and a 15 second request deadline. Application handlers
// must also bound produced objects and honor cancellation; Go cannot forcibly
// stop an uncooperative in-process callback.
type Limits struct {
	RequestBytes int64
	// ResponseBytes must be at least 4096, reserving enough encoded space for
	// an exact maximum-length recovery reference and bounded failure guidance.
	ResponseBytes int64
	Concurrent    int
	Timeout       time.Duration
}

// Config is copied by New. HTTPS is required except for loopback development.
// The canonical public Host must be preserved by an ingress proxy; forwarded
// headers never establish authority. Scopes are all required for admission.
type Config struct {
	ResourceURL          string
	AuthorizationServers []string
	Scopes               []string
	AllowedOrigins       []string
	Verify               VerifyToken
	// Admit checks current administrator enablement and client/user connection
	// admission on every authenticated HTTP request. ErrDenied is a deliberate
	// denial; other errors mean admission cannot currently be evaluated.
	Admit func(context.Context, Caller) error
	// Gate can only narrow access to a registered operation. The ordinary
	// application service must still authorize the verified principal itself.
	Gate func(context.Context, Caller, string) error
	// Diagnostic is required. It receives protected original causes and one
	// terminal event per operation, including success. It must be concurrent,
	// honor context and own access, storage, export, retention and repetition
	// budgets. It is not an atomic business audit record.
	Diagnostic func(context.Context, Diagnostic)
	Limits     Limits
}

// Endpoint is an immutable resource-server HTTP handler. Construction and idle
// registration start no background work. Mount MetadataHandler at the RFC 9728
// well-known path derived from ResourceURL, for example
// /.well-known/oauth-protected-resource/mcp for https://example.com/mcp.
type Endpoint struct {
	config      Config
	resource    *url.URL
	metadataURL string
	origins     map[string]bool
	active      chan struct{}
	handler     http.Handler
	metadata    http.Handler
	inputs      map[string]*inputDiagnostic
	jsonHTTP    bool
}

// New validates configuration and completes fixed tool registration before
// exposing the endpoint. The Registrar is valid only inside register.
func New(config Config, register func(*Registrar) error) (*Endpoint, error) {
	if sessionCompatibility {
		return nil, errors.New("bridge: MCPGODEBUG allowsessionsinstateless=1 is incompatible with the request-only profile")
	}
	if register == nil {
		return nil, errors.New("bridge: tool registration is required")
	}
	e, err := newEndpoint(config)
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "achrix-bridge", Version: Version()}, &mcp.ServerOptions{
		Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: false}},
		SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25"},
		SetCacheable:              func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) { c.CacheScope = "private"; c.TTLMs = 0 },
	})
	registrar := &Registrar{server: server, endpoint: e, names: e.inputs, open: true}
	err = register(registrar)
	registrar.mu.Lock()
	registrar.open = false
	registrar.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("bridge: register tools: %w", err)
	}
	server.AddReceivingMiddleware(e.receive)
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: e.config.Limits.RequestBytes, PropagateRequestCancellation: true,
	})
	e.handler = e.protect(transport)
	return e, nil
}

// newEndpoint owns transport-independent configuration and immutable metadata.
// Protocol constructors select only their routing and invocation profile.
func newEndpoint(config Config) (*Endpoint, error) {
	if config.Verify == nil || config.Admit == nil || config.Diagnostic == nil {
		return nil, errors.New("bridge: Verify, Admit and Diagnostic are required")
	}
	resource, err := canonicalURL(config.ResourceURL)
	if err != nil {
		return nil, fmt.Errorf("bridge: resource URL: %w", err)
	}
	if len(config.AuthorizationServers) == 0 || len(config.AuthorizationServers) > 8 {
		return nil, errors.New("bridge: provide 1 to 8 authorization server issuers")
	}
	config.AuthorizationServers = append([]string(nil), config.AuthorizationServers...)
	for _, issuer := range config.AuthorizationServers {
		if _, err := canonicalURL(issuer); err != nil {
			return nil, fmt.Errorf("bridge: issuer URL: %w", err)
		}
	}
	config.Scopes = append([]string(nil), config.Scopes...)
	if !validScopes(config.Scopes) {
		return nil, errors.New("bridge: scopes must be bounded OAuth scope tokens")
	}
	if err := normalizeLimits(&config.Limits); err != nil {
		return nil, err
	}
	if len(config.AllowedOrigins) > 16 {
		return nil, errors.New("bridge: at most 16 trusted origins are supported")
	}
	config.AllowedOrigins = append([]string(nil), config.AllowedOrigins...)
	origins := map[string]bool{resource.Scheme + "://" + resource.Host: true}
	for _, origin := range config.AllowedOrigins {
		u, err := canonicalURL(origin)
		if err != nil || u.Path != "" {
			return nil, errors.New("bridge: trusted origins must be absolute HTTPS origins without paths")
		}
		origins[u.Scheme+"://"+u.Host] = true
	}
	meta := *resource
	meta.Path = "/.well-known/oauth-protected-resource"
	if resource.Path != "/" {
		meta.Path += resource.Path
	}
	meta.RawPath = ""
	e := &Endpoint{config: config, resource: resource, metadataURL: meta.String(), origins: origins, active: make(chan struct{}, config.Limits.Concurrent), inputs: make(map[string]*inputDiagnostic)}
	e.metadata = auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: config.ResourceURL, AuthorizationServers: config.AuthorizationServers,
		ScopesSupported: config.Scopes, BearerMethodsSupported: []string{"header"},
	})
	return e, nil
}

func (e *Endpoint) protect(next http.Handler) http.Handler {
	return auth.RequireBearerToken(e.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: e.metadataURL, Scopes: e.config.Scopes,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := stateFromContext(r.Context())
		if err := r.Context().Err(); err != nil {
			e.failHTTP(w, r, state, http.StatusServiceUnavailable, "admission", timeoutFault(NotExecuted), err)
			return
		}
		if err := e.config.Admit(r.Context(), state.request.Caller); err != nil {
			fault := admissionFault(err)
			e.failHTTP(w, r, state, httpStatus(fault), "admission", fault, err)
			return
		}
		if err := r.Context().Err(); err != nil {
			e.failHTTP(w, r, state, http.StatusServiceUnavailable, "admission", timeoutFault(NotExecuted), err)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// MetadataHandler serves public, configured resource metadata. It performs no
// discovery fetch, does not infer issuer/host from the request and issues no token.
func (e *Endpoint) MetadataHandler() http.Handler { return e.metadata }

func canonicalURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 2048 {
		return nil, errors.New("URL is empty or too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, errors.New("URL must be absolute, without credentials, query, fragment or encoded path")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, errors.New("HTTPS is required except on loopback development hosts")
	}
	if strings.ContainsAny(u.Host, "\r\n\t ") || strings.Contains(u.Path, "//") {
		return nil, errors.New("URL must have a canonical host and path")
	}
	if u.Path != "" && u.Path != path.Clean(u.Path) && u.Path != path.Clean(u.Path)+"/" {
		return nil, errors.New("URL path contains noncanonical segments")
	}
	return u, nil
}

func loopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func normalizeLimits(l *Limits) error {
	if l.RequestBytes == 0 {
		l.RequestBytes = 64 << 10
	}
	if l.ResponseBytes == 0 {
		l.ResponseBytes = 1 << 20
	}
	if l.Concurrent == 0 {
		l.Concurrent = 16
	}
	if l.Timeout == 0 {
		l.Timeout = 15 * time.Second
	}
	if l.RequestBytes < 256 || l.RequestBytes > 16<<20 || l.ResponseBytes < 4096 || l.ResponseBytes > 16<<20 || l.Concurrent < 1 || l.Concurrent > 1024 || l.Timeout < time.Millisecond || l.Timeout > 5*time.Minute {
		return errors.New("bridge: limits must be positive and bounded (input 256 B–16 MiB, output 4 KiB–16 MiB, concurrency 1–1024, timeout 1 ms–5 min)")
	}
	return nil
}

func boundedText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func validScopes(scopes []string) bool {
	if len(scopes) > 32 {
		return false
	}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if len(scope) == 0 || len(scope) > 128 || seen[scope] {
			return false
		}
		for _, c := range scope {
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				return false
			}
		}
		seen[scope] = true
	}
	return true
}
