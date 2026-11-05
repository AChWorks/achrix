// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func TestSDKProfileRejectsUnsupportedOIDCAndClientCapabilities(t *testing.T) {
	c := publicTestConfig()
	if _, err := New(c); err != nil {
		t.Fatal(err)
	}
	good := sdkClient{Client: c.Clients[0]}
	for _, tc := range []struct {
		name      string
		scopes    []string
		method    oidc.CodeChallengeMethod
		challenge string
		redirect  string
		wantError bool
	}{
		{"code_s256", []string{"notes.read"}, oidc.CodeChallengeMethodS256, testChallenge(), testCallback, false},
		{"plain_not_supported", []string{"notes.read"}, oidc.CodeChallengeMethodPlain, testVerifier, testCallback, false},
		{"bad_redirect", []string{"notes.read"}, oidc.CodeChallengeMethodS256, testChallenge(), "https://evil.example.test", true},
		{"wrong_response_scope", []string{"unauthorized"}, oidc.CodeChallengeMethodS256, testChallenge(), testCallback, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &oidc.AuthRequest{ClientID: good.GetID(), RedirectURI: tc.redirect, ResponseType: oidc.ResponseTypeCode,
				CodeChallenge: tc.challenge, CodeChallengeMethod: tc.method, Scopes: oidc.SpaceDelimitedArray(tc.scopes)}
			_, err := op.ValidateAuthRequestClient(context.Background(), req, good, nil)
			if tc.wantError && err == nil {
				t.Fatal("upstream validation unexpectedly accepted")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("upstream validation failed: %v", err)
			}
			if tc.name == "wrong_response_scope" && len(req.Scopes) > 0 {
				t.Fatalf("SDK strips unavailable scopes rather than rejecting: %+v", req.Scopes)
			}
			if tc.name == "plain_not_supported" {
				query := url.Values{"response_type": {"code"}, "client_id": {good.ID}, "redirect_uri": {testCallback}, "scope": {"notes.read"}, "resource": {testResource}, "code_challenge": {testVerifier}, "code_challenge_method": {"plain"}}
				request := httptest.NewRequest(http.MethodGet, testIssuer+"/authorize?"+query.Encode(), nil)
				record := httptest.NewRecorder()
				server, _ := New(c)
				server.beginAuthorize(record, request)
				if record.Code != http.StatusSeeOther || !strings.Contains(record.Header().Get("Location"), "invalid_request") {
					t.Fatalf("AChrix guard failed to reject plain: %d %q", record.Code, record.Header().Get("Location"))
				}
			}
		})
	}
	c.Clients[0].Scopes = []string{"openid"}
	if _, err := New(c); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("advertised unsupported openid accepted: %v", err)
	}
}

func TestClientLookupMustStayCurrentAndNarrow(t *testing.T) {
	c := publicTestConfig()
	current := c.Clients[0]
	available := true
	c.ClientLookup = func(ctx context.Context, id string) (Client, error) {
		if !available {
			return Client{}, errors.New("product client registry outage")
		}
		if id != current.ID {
			return Client{}, ErrInvalidGrant
		}
		return current, nil
	}
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.client(context.Background(), current.ID); err != nil {
		t.Fatal(err)
	}
	current.Scopes = []string{"notes.read"}
	active, err := s.client(context.Background(), current.ID)
	if err != nil || len(active.Scopes) != 1 || active.Scopes[0] != "notes.read" {
		t.Fatalf("scope reduction not current: %v %+v", err, active)
	}
	active.Scopes[0] = "tampered"
	if current.Scopes[0] != "notes.read" {
		t.Fatal("lookup returned shared mutable registry slice")
	}
	current.RedirectURIs = []string{"https://other.example.test"}
	if _, err = s.client(context.Background(), current.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("scope ceiling expanded: %v", err)
	}
	current = c.Clients[0]
	current.Enabled = false
	if _, err = s.client(context.Background(), current.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("disabled client accepted: %v", err)
	}
	current = c.Clients[0]
	available = false
	if _, err = s.client(context.Background(), current.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("registry failure did not fail closed: %v", err)
	}
}

func TestConfidentialBasicRFC6749FormDecoding(t *testing.T) {
	secret := strings.Repeat("ab", 18) + "%+:& /?"
	c := publicTestConfig()
	registered := Client{ID: "confidential", Name: "Registration", Enabled: true, Confidential: true, SecretDigest: sha256.Sum256([]byte(secret)),
		RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read"}}
	c.Clients = append(c.Clients, registered)
	c.ClientLookup = func(_ context.Context, id string) (Client, error) {
		if id != "confidential" {
			return Client{}, ErrInvalidGrant
		}
		return registered, nil
	}
	server, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"grant_type": {"authorization_code"}}
	good := httptest.NewRequest(http.MethodPost, testIssuer+"/token", nil)
	good.SetBasicAuth(url.QueryEscape("confidential"), url.QueryEscape(secret))
	if _, err := server.authenticateClient(good, values); err != nil {
		t.Fatal("compliant RFC6749 Basic encoding rejected")
	}
	invalid := httptest.NewRequest(http.MethodPost, testIssuer+"/token", nil)
	invalid.SetBasicAuth("confidential", "%invalid%")
	if _, err := server.authenticateClient(invalid, values); err == nil {
		t.Fatal("malformed percent encoding accepted")
	}
	raw := httptest.NewRequest(http.MethodPost, testIssuer+"/token", nil)
	raw.SetBasicAuth("confidential", secret)
	if _, err := server.authenticateClient(raw, values); err == nil {
		t.Fatal("unescaped reserved Basic secret accepted")
	}
}
