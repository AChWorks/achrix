// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testIssuer = "https://issuer.example.test"
const testResource = "https://product.example.test/mcp"
const testCallback = "https://client.example.test/callback"

// This HTML is deliberately test-only: real products own their UX,
// localization, branding and accessibility. No HTML is compiled into OAuth.
func testRenderConsent(w http.ResponseWriter, r *http.Request, v ConsentView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html lang='en'><head><meta charset='utf-8'><title>Authorize access</title></head><body><main><h1>Authorize access</h1><p>Signed in as %s. %s requests %s on %s.</p><form method='post' action='%s'><input type='hidden' name='transaction' value='%s'><input type='hidden' name='csrf' value='%s'><button name='decision' value='allow' type='submit'>Approve</button><button name='decision' value='deny' type='submit'>Deny</button></form></main></body></html>",
		html.EscapeString(v.Subject), html.EscapeString(v.ClientName), html.EscapeString(strings.Join(v.Scopes, ", ")),
		html.EscapeString(v.Resource), html.EscapeString(v.FormAction), html.EscapeString(v.Transaction), html.EscapeString(v.CSRF))
}
func publicTestConfig() Config {
	return Config{
		Issuer: testIssuer, Resource: testResource, Pool: &pgxpool.Pool{},
		Clients: []Client{{ID: "public.example", Name: "A client", Enabled: true, RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read", "notes.write"}}},
		Session: func(context.Context, *http.Request) (Session, error) {
			return Session{Subject: "user-1", Binding: sha256.Sum256([]byte("session-1"))}, nil
		},
		Permit:        func(context.Context, Grant) error { return nil },
		RenderConsent: testRenderConsent,
		Diagnostic:    func(context.Context, string, string, error) {},
	}
}
func TestConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		bad  func(*Config)
	}{
		{"HTTP issuer", func(c *Config) { c.Issuer = "http://issuer.example.test" }},
		{"issuer query", func(c *Config) { c.Issuer += "?spoof=1" }},
		{"issuer fragment", func(c *Config) { c.Issuer += "#fragment" }},
		{"issuer user info", func(c *Config) { c.Issuer = "https://user@issuer.example.test" }},
		{"issuer dot segment", func(c *Config) { c.Issuer = testIssuer + "/a/../issuer" }},
		{"issuer double separator", func(c *Config) { c.Issuer = testIssuer + "/a//issuer" }},
		{"issuer encoded path", func(c *Config) { c.Issuer = testIssuer + "/%61" }},
		{"issuer oversized", func(c *Config) { c.Issuer = testIssuer + "/" + strings.Repeat("x", 2100) }},
		{"noncanonical issuer", func(c *Config) { c.Issuer += "," }},
		{"invalid resource", func(c *Config) { c.Resource = "http://product.example.test/mcp" }},
		{"resource dot segment", func(c *Config) { c.Resource = testResource + "/../other" }},
		{"resource double separator", func(c *Config) { c.Resource = "https://product.example.test/a//mcp" }},
		{"resource encoded path", func(c *Config) { c.Resource = "https://product.example.test/%6dcp" }},
		{"resource oversized", func(c *Config) { c.Resource = testResource + "/" + strings.Repeat("x", 2100) }},
		{"missing clients", func(c *Config) { c.Clients = nil }},
		{"unknown pool", func(c *Config) { c.Pool = nil }},
		{"missing session adapter", func(c *Config) { c.Session = nil }},
		{"missing permission adapter", func(c *Config) { c.Permit = nil }},
		{"missing diagnostics", func(c *Config) { c.Diagnostic = nil }},
		{"missing consent renderer", func(c *Config) { c.RenderConsent = nil }},
		{"wildcard redirect", func(c *Config) { c.Clients[0].RedirectURIs = []string{"https://*.example.test/callback"} }},
		{"redirect code conflict", func(c *Config) { c.Clients[0].RedirectURIs = []string{testCallback + "?code=leak"} }},
		{"redirect fragment", func(c *Config) { c.Clients[0].RedirectURIs = []string{testCallback + "#part"} }},
		{"redirect dot segment", func(c *Config) { c.Clients[0].RedirectURIs = []string{"https://client.example.test/a/../callback"} }},
		{"redirect double separator", func(c *Config) { c.Clients[0].RedirectURIs = []string{"https://client.example.test/a//callback"} }},
		{"redirect encoded path", func(c *Config) { c.Clients[0].RedirectURIs = []string{"https://client.example.test/%63allback"} }},
		{"redirect oversized", func(c *Config) {
			c.Clients[0].RedirectURIs = []string{testCallback + "?q=" + strings.Repeat("x", 2100)}
		}},
		{"client duplicated", func(c *Config) { c.Clients = append(c.Clients, c.Clients[0]) }},
		{"client colon invalid", func(c *Config) { c.Clients[0].ID = "public:unsafe" }},
		{"client URL invalid", func(c *Config) { c.Clients[0].ID = "https://client.example" }},
		{"invalid scope", func(c *Config) { c.Clients[0].Scopes = []string{"notes write"} }},
		{"public client with credential", func(c *Config) { c.Clients[0].SecretDigest = sha256.Sum256([]byte("fake-secret")) }},
		{"confidential client without credential", func(c *Config) { c.Clients[0].Confidential = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := publicTestConfig()
			tc.bad(&c)
			if _, err := New(c); !errors.Is(err, ErrConfiguration) {
				t.Errorf("unexpected config accepted: %v", err)
			}
		})
	}
}
func TestServerMetadataAndHostBoundary(t *testing.T) {
	c := publicTestConfig()
	c.Issuer = "https://issuer.example.test/tenant"
	c.Clients = append(c.Clients, Client{ID: "confidential.example", Confidential: true, SecretDigest: sha256.Sum256([]byte(strings.Repeat("q", 40))), Enabled: true, RedirectURIs: []string{"https://client2.example.test/return"}, Scopes: []string{"notes.read"}})
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if s.MetadataPath() != "/.well-known/oauth-authorization-server/tenant" {
		t.Fatalf("path: %q", s.MetadataPath())
	}
	req := httptest.NewRequest(http.MethodGet, c.IssuerOrigin()+s.MetadataPath(), nil)
	req.TLS = &tls.ConnectionState{}
	req.URL.Host = ""
	req.URL.Scheme = ""
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metadata status %d: %s", recorder.Code, recorder.Body)
	}
	var got map[string]any
	if err = json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"issuer":                 c.Issuer,
		"authorization_endpoint": c.Issuer + "/authorize",
		"token_endpoint":         c.Issuer + "/token",
		"revocation_endpoint":    c.Issuer + "/revoke",
		"authorization_response_iss_parameter_supported": true,
	} {
		if got[k] != want {
			t.Errorf("%s=%v want %v", k, got[k], want)
		}
	}
	for k, want := range map[string][]string{
		"response_types_supported":                   {"code"},
		"grant_types_supported":                      {"authorization_code", "refresh_token"},
		"response_modes_supported":                   {"query"},
		"code_challenge_methods_supported":           {"S256"},
		"token_endpoint_auth_methods_supported":      {"client_secret_basic", "none"},
		"revocation_endpoint_auth_methods_supported": {"client_secret_basic", "none"},
	} {
		raw, ok := got[k].([]any)
		if !ok {
			t.Errorf("%s absent", k)
			continue
		}
		actual := []string{}
		for _, item := range raw {
			actual = append(actual, item.(string))
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("%s=%v want %v", k, actual, want)
		}
	}
	if !strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("metadata cacheable")
	}
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.Host = "evil.example" },
		func(r *http.Request) { r.TLS = nil },
		func(r *http.Request) { r.URL.RawPath = "/untrusted" },
	} {
		req := httptest.NewRequest(http.MethodGet, c.IssuerOrigin()+s.MetadataPath(), nil)
		req.TLS = &tls.ConnectionState{}
		req.URL.Host = ""
		req.URL.Scheme = ""
		edit(req)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("accepted hostile ingress")
		}
	}
}
func (c Config) IssuerOrigin() string { u, _ := url.Parse(c.Issuer); return u.Scheme + "://" + u.Host }
func TestOpaqueTokenAndParserBounds(t *testing.T) {
	a, err := token()
	if err != nil {
		t.Fatal(err)
	}
	b, err := token()
	if err != nil {
		t.Fatal(err)
	}
	if !validToken(a) || a == b || len(hash(a)) != 32 {
		t.Fatal("bad token generation")
	}
	for _, v := range []string{"", "abc", strings.Repeat("Z", 43) + "="} {
		if validToken(v) {
			t.Error("invalid token accepted")
		}
	}
	if _, err = requestForm(httptest.NewRequest(http.MethodPost, testIssuer+"/token", strings.NewReader("grant_type=x&grant_type=y"))); err == nil {
		t.Fatal("missing media type accepted")
	}
	request := httptest.NewRequest(http.MethodPost, testIssuer+"/token", strings.NewReader(strings.Repeat("x", 9000)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err = requestForm(request); err == nil {
		t.Fatal("oversized form accepted")
	}
}
func TestServerNoAutomaticExpiryOrBackgroundWork(t *testing.T) {
	cfg := publicTestConfig()
	cfg.Now = func() time.Time { return time.Now() }
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.slots) != 0 {
		t.Fatal("idle constructor consumed request slot")
	}
}
