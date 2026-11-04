// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The OAuth authorization server is deliberately not part of Bridge. This
// regression checks the real resource-server discovery seam consumed by a
// future independently composed issuer, without manufacturing OAuth tokens.
func TestOAuthProtectedResourceMetadataIsCanonicalAndPublic(t *testing.T) {
	endpoint, err := New(unitConfig(), func(*Registrar) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://different.example/.well-known/oauth-protected-resource/mcp", nil)
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.Header.Set("X-Forwarded-Proto", "http")
	recorder := httptest.NewRecorder()
	endpoint.MetadataHandler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("invalid protected resource metadata response: status=%d type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var metadata struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
		BearerMethods        []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Resource != "https://product.example/mcp" ||
		!reflect.DeepEqual(metadata.AuthorizationServers, []string{"https://issuer.example"}) ||
		!reflect.DeepEqual(metadata.ScopesSupported, []string{"product"}) ||
		!reflect.DeepEqual(metadata.BearerMethods, []string{"header"}) {
		t.Fatalf("resource metadata was not fixed by trusted configuration: %+v", metadata)
	}
	if strings.Contains(recorder.Body.String(), "attacker.example") ||
		strings.Contains(recorder.Body.String(), "different.example") {
		t.Fatal("untrusted host or forwarded headers changed public metadata")
	}
	// An unexpected Host is rejected before authentication and must not be
	// used as a source of discovery authority.
	mismatched := httptest.NewRecorder()
	wrongHost := httptest.NewRequest(http.MethodPost, "https://attacker.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	wrongHost.Header.Set("X-Forwarded-Host", "attacker.example")
	wrongHost.Header.Set("X-Forwarded-Proto", "http")
	wrongHost.Header.Set("Content-Type", "application/json")
	wrongHost.Header.Set("Accept", "application/json, text/event-stream")
	wrongHost.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	endpoint.ServeHTTP(mismatched, wrongHost)
	if mismatched.Code != http.StatusForbidden {
		t.Fatalf("mismatched Host accepted by protected resource: status=%d", mismatched.Code)
	}

	// Spoofed forwarding headers cannot rewrite the standard challenge even
	// for a correctly hosted request that is missing credentials.
	denied := httptest.NewRecorder()
	protected := httptest.NewRequest(http.MethodPost, "https://product.example/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	protected.Header.Set("X-Forwarded-Host", "attacker.example")
	protected.Header.Set("X-Forwarded-Proto", "http")
	protected.Header.Set("Content-Type", "application/json")
	protected.Header.Set("Accept", "application/json, text/event-stream")
	protected.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	endpoint.ServeHTTP(denied, protected)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated protected operation returned %d, want 401", denied.Code)
	}
	challenge := denied.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `resource_metadata="https://product.example/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("challenge did not point to trusted protected resource metadata: %q", challenge)
	}
}
