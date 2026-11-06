// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCIMDURL = "https://chatgpt.com/oauth/client.json"

// This candidate supports explicitly pre-enrolled URL identifiers, not
// arbitrary client discovery. Registration is an operator trust decision.
func TestEnrolledCIMDClientIDValidation(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"public.example", true},
		{testCIMDURL, true},
		{"https://claude.ai/oauth/client-metadata", true},
		{"https://client.example.com", false}, // document path required
		{"https://client.example.com/", false},
		{"http://chatgpt.com/oauth/client.json", false},
		{"https://127.0.0.1/oauth/client.json", false},
		{"https://[::1]/oauth/client.json", false},
		{"https://localhost/oauth/client.json", false},
		{"https://a.localhost/oauth/client.json", false},
		{"https://private.internal/oauth/client.json", false},
		{"https://user@chatgpt.com/oauth/client.json", false},
		{"https://chatgpt.com:443/oauth/client.json", false},
		{"https://chatgpt.com:/oauth/client.json", false}, // empty explicit port must not be canonical
		{"https://chatgpt.com:/oauth/client.json?", false},
		{"https://chatgpt.com/oauth/client.json?", false}, // empty query delimiter is still a query
		{"https://chatgpt.com/oauth/client.json?key=x", false},
		{"https://chatgpt.com/oauth/client.json#fragment", false},
		{"https://chatgpt.com/a/../oauth/client.json", false},
		{"https://chatgpt.com//oauth/client.json", false},
		{"https://chatgpt.com/%6fauth/client.json", false},
		{"https://CHATGPT.com/oauth/client.json", false},
		{"client:id", false},
		{strings.Repeat("x", 201), false},
		{"https://chatgpt.com/" + strings.Repeat("x", 245), false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if got := validClientID(tc.id); got != tc.want {
				t.Errorf("validClientID returned %t, want %t", got, tc.want)
			}
		})
	}
}

func TestEnrolledCIMDConfiguredMetadataAndTrustCeiling(t *testing.T) {
	c := publicTestConfig()
	newClient := Client{ID: testCIMDURL, Name: "Pre-enrolled OpenAI client", RedirectURIs: []string{testCallback},
		Scopes: []string{"notes.read"}, Enabled: true}
	c.Clients = append(c.Clients, newClient)
	// Product-approved live metadata must be retrieved by ID. The API never
	// learns new registrations from client-supplied URLs.
	c.ClientLookup = func(_ context.Context, id string) (Client, error) {
		if id == testCIMDURL {
			return newClient, nil
		}
		return Client{}, ErrInvalidGrant
	}
	s, err := New(c)
	if err != nil {
		t.Fatalf("pre-enrolled metadata URL invalid: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, c.IssuerOrigin()+s.MetadataPath(), nil)
	req.TLS = &tls.ConnectionState{}
	req.URL.Host, req.URL.Scheme = "", ""
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("issuer metadata status %d", rec.Code)
	}
	var metadata map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["client_id_metadata_document_supported"] != true {
		t.Fatal("pre-enrolled URL client profile must be discoverable")
	}
	if _, err := s.client(context.Background(), "https://unregistered.example.com/client.json"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatal("unregistered URL admitted")
	}
	if c2, err := s.client(context.Background(), testCIMDURL); err != nil || c2.ID != testCIMDURL {
		t.Fatalf("registered URL client not identified: %v", err)
	}
	confidential := c
	confidential.Clients = append([]Client(nil), c.Clients...)
	confidential.Clients[1].Confidential = true
	confidential.Clients[1].SecretDigest = SecretDigest(strings.Repeat("C", 40))
	if _, err := New(confidential); !errors.Is(err, ErrConfiguration) {
		t.Fatal("URL client incorrectly accepted unsupported confidential method")
	}
}

func TestOpaqueOnlyIssuerDoesNotAdvertiseCIMD(t *testing.T) {
	c := publicTestConfig()
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, c.IssuerOrigin()+s.MetadataPath(), nil)
	req.TLS = &tls.ConnectionState{}
	req.URL.Host, req.URL.Scheme = "", ""
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var metadata map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &metadata) != nil {
		t.Fatalf("unexpected metadata result: %d", rec.Code)
	}
	if _, advertised := metadata["client_id_metadata_document_supported"]; advertised {
		t.Fatal("CIMD advertised without pre-enrolled URL client")
	}
}

func TestPostgreSQL18MinorUpgradeGate(t *testing.T) {
	for _, tc := range []struct {
		version  int
		encoding string
		allowed  bool
	}{
		{180006, "UTF8", true},
		{180007, "UTF8", true},
		{180100, "UTF8", true},
		{180005, "UTF8", false},
		{170999, "UTF8", false},
		{190000, "UTF8", false},
		{180006, "SQL_ASCII", false},
	} {
		if got := supportedPostgreSQL18(tc.version, tc.encoding); got != tc.allowed {
			t.Errorf("version %d encoding %s allowed=%t want=%t", tc.version, tc.encoding, got, tc.allowed)
		}
	}
}
