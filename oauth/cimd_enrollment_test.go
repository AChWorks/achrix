// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func checkedClient() Client {
	return Client{ID: testCIMDURL, Name: "QA", Enabled: true, RedirectURIs: []string{"https://chatgpt.com/connector_platform_oauth_redirect"}, Scopes: []string{"notes.read"}}
}
func checkedMetadata(t *testing.T, changes map[string]any) []byte {
	t.Helper()
	d := map[string]any{
		"client_id":                             testCIMDURL,
		"redirect_uris":                         []string{"https://chatgpt.com/connector_platform_oauth_redirect"},
		"token_endpoint_auth_methods_supported": []string{"none", "private_key_jwt"},
		"token_endpoint_auth_method":            "private_key_jwt",
		"response_types":                        []string{"code"},
		"grant_types":                           []string{"authorization_code", "refresh_token"},
		"jwks_uri":                              "https://chatgpt.com/oauth/jwks.json",
	}
	for k, v := range changes {
		if v == nil {
			delete(d, k)
		} else {
			d[k] = v
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestPreEnrolledCIMDCompatibility(t *testing.T) {
	positives := []struct {
		name    string
		changes map[string]any
	}{
		{"chatgpt_method_intersection", nil},
		{"other_published_callback_does_not_grant_privileges", map[string]any{"redirect_uris": []string{"https://chatgpt.com/connector_platform_oauth_redirect", "https://chatgpt.com/other"}}},
		{"cimd_none_auth_default", map[string]any{"token_endpoint_auth_methods_supported": nil, "token_endpoint_auth_method": nil, "grant_types": nil, "response_types": nil}},
		{"unselected_future_auth_method", map[string]any{"token_endpoint_auth_methods_supported": []string{"none", "future_asymmetric_method"}, "token_endpoint_auth_method": nil}},
		{"future_auth_preferred_but_none_available", map[string]any{"token_endpoint_auth_methods_supported": []string{"none", "future_asymmetric_method"}, "token_endpoint_auth_method": "future_asymmetric_method"}},
	}
	for _, tc := range positives {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePreEnrolledClientMetadata(checkedClient(), checkedMetadata(t, tc.changes)); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalid := []struct {
		name   string
		change map[string]any
	}{
		{"id_mismatch", map[string]any{"client_id": "https://evil.example.com/client.json"}},
		{"id_missing", map[string]any{"client_id": nil}},
		{"id_type", map[string]any{"client_id": 7}},
		{"redirect_removed", map[string]any{"redirect_uris": []string{"https://attacker.example.com/cb"}}},
		{"redirect_missing", map[string]any{"redirect_uris": nil}},
		{"redirect_empty", map[string]any{"redirect_uris": []string{}}},
		{"redirect_wrong_type", map[string]any{"redirect_uris": "somewhere"}},
		{"redirect_duplicate", map[string]any{"redirect_uris": []string{"https://chatgpt.com/connector_platform_oauth_redirect", "https://chatgpt.com/connector_platform_oauth_redirect"}}},
		{"method_missing_none", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"private_key_jwt"}}},
		{"only_legacy_key_preference", map[string]any{"token_endpoint_auth_methods_supported": nil}},
		{"legacy_preference_not_supported", map[string]any{"token_endpoint_auth_methods_supported": []string{"none"}}},
		{"shared_secret_method", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic"}}},
		{"shared_secret_post_method", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"}}},
		{"shared_secret_jwt_method", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_jwt"}}},
		{"empty_method_string", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", ""}}},
		{"long_method_string", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", strings.Repeat("x", 129)}}},
		{"newline_method_string", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", "future\nmethod"}}},
		{"empty_methods", map[string]any{"token_endpoint_auth_methods_supported": []string{}}},
		{"duplicate_methods", map[string]any{"token_endpoint_auth_method": nil, "token_endpoint_auth_methods_supported": []string{"none", "none"}}},
		{"auth_wrong_type", map[string]any{"token_endpoint_auth_methods_supported": "none"}},
		{"grant_no_code", map[string]any{"grant_types": []string{"client_credentials"}}},
		{"response_no_code", map[string]any{"response_types": []string{"token"}}},
		{"grant_wrong_type", map[string]any{"grant_types": "authorization_code"}},
		{"client_secret", map[string]any{"client_secret": "secret"}},
		{"secret_expiration", map[string]any{"client_secret_expires_at": 0}},
		{"embedded_jwks", map[string]any{"jwks": map[string]any{"keys": []any{}}}},
		{"software_statement", map[string]any{"software_statement": "untrusted"}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePreEnrolledClientMetadata(checkedClient(), checkedMetadata(t, tc.change)); !errors.Is(err, ErrConfiguration) {
				t.Fatalf("metadata accepted: %v", err)
			}
		})
	}
}

func TestPreEnrolledCIMDBadDocumentsAndRegistration(t *testing.T) {
	cases := []struct {
		name string
		doc  []byte
	}{
		{"malformed", []byte("{")},
		{"duplicate_top_level_id", []byte("{\"client_id\":\"https://chatgpt.com/oauth/client.json\",\"client_id\":\"https://chatgpt.com/oauth/client.json\"}")},
		{"trailing_JSON", []byte("{\"client_id\":\"https://chatgpt.com/oauth/client.json\"} {}")},
		{"invalid_utf8", []byte{0x7b, 0x22, 0xff, 0x22, 0x3a, 0x31, 0x7d}},
		{"oversized", []byte(strings.Repeat(" ", 65537))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePreEnrolledClientMetadata(checkedClient(), tc.doc); !errors.Is(err, ErrConfiguration) {
				t.Fatalf("invalid document accepted: %v", err)
			}
		})
	}
	regs := []struct {
		name   string
		change func(*Client)
	}{
		{"confidential", func(c *Client) { c.Confidential = true }},
		{"unexpected_secret_digest", func(c *Client) { c.SecretDigest = SecretDigest(strings.Repeat("x", 32)) }},
		{"opaque_id", func(c *Client) { c.ID = "opaque.example" }},
		{"bad_registered_redirect", func(c *Client) { c.RedirectURIs = []string{"http://chatgpt.com/callback"} }},
		{"duplicate_registered_redirect", func(c *Client) { c.RedirectURIs = append(c.RedirectURIs, c.RedirectURIs[0]) }},
	}
	for _, tc := range regs {
		t.Run(tc.name, func(t *testing.T) {
			c := checkedClient()
			tc.change(&c)
			if err := ValidatePreEnrolledClientMetadata(c, checkedMetadata(t, nil)); !errors.Is(err, ErrConfiguration) {
				t.Fatalf("bad registration accepted: %v", err)
			}
		})
	}
}
func TestPreEnrolledCIMDDriftFailClosedViaProductLookup(t *testing.T) {
	client := checkedClient()
	document := checkedMetadata(t, nil)
	approved := false

	// The product performs verification out of band. ClientLookup only
	// checks a locally authoritative enrollment decision on request paths.
	// The production source of this decision must be durable product state.
	verifyDocument := func() error {
		err := ValidatePreEnrolledClientMetadata(client, document)
		if err != nil {
			approved = false // sticky disable until explicit new approval
		}
		return err
	}
	if err := verifyDocument(); err != nil {
		t.Fatal(err)
	}
	approved = true // initial operator approval
	config := publicTestConfig()
	config.Clients = []Client{client}
	config.ClientLookup = func(_ context.Context, id string) (Client, error) {
		if id != client.ID || !approved {
			return Client{}, ErrInvalidGrant
		}
		return client, nil
	}
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.client(context.Background(), client.ID); err != nil {
		t.Fatal(err)
	}
	document = checkedMetadata(t, map[string]any{"redirect_uris": []string{"https://attacker.example.com/cb"}})
	if err := verifyDocument(); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("metadata drift not detected: %v", err)
	}
	if _, err = s.client(context.Background(), client.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("disabled client admitted: %v", err)
	}
	document = checkedMetadata(t, nil)
	if err := verifyDocument(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.client(context.Background(), client.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("silent auto reactivation: %v", err)
	}
	approved = true // explicit operator action after new verification
	if _, err = s.client(context.Background(), client.ID); err != nil {
		t.Fatalf("reapproved client rejected: %v", err)
	}
}
