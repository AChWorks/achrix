// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRealHTTPRevocationDoesNotRevealUnknownTokens(t *testing.T) {
	f := newFixture(t)
	aCode, _ := f.consent("public.example", "alice")
	alice := f.tokens("public.example", "", "authorization_code", aCode)
	bCode, _ := f.consent("confidential.example", "bob")
	bob := f.tokens("confidential.example", strings.Repeat("s", 40), "authorization_code", bCode)
	call := func(client, secret, raw string) int {
		t.Helper()
		form := url.Values{"client_id": {client}, "token": {raw}, "token_type_hint": {"refresh_token"}}
		req, err := http.NewRequest(http.MethodPost, f.web.URL+f.srv.RevokePath(), strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if secret != "" {
			req.SetBasicAuth(client, secret)
		}
		response, err := f.browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := call("public.example", "", bob.RefreshToken); got != http.StatusOK {
		t.Fatalf("foreign grant existence leak %d", got)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, bob.AccessToken); err != nil {
		t.Fatal("foreign client revoked:", err)
	}
	if got := call("confidential.example", "wrong", bob.RefreshToken); got != http.StatusUnauthorized {
		t.Fatalf("weak client credential accepted: %d", got)
	}
	if got := call("public.example", "", "not-a-token"); got != http.StatusOK {
		t.Fatalf("token existence leak: %d", got)
	}
	if got := call("public.example", "", alice.RefreshToken); got != http.StatusOK {
		t.Fatalf("public client revocation HTTP %d", got)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, alice.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("access not revoked: %v", err)
	}
	if got := call("public.example", "", alice.RefreshToken); got != http.StatusOK {
		t.Fatalf("duplicate revocation HTTP %d", got)
	}
	if got := call("confidential.example", strings.Repeat("s", 40), bob.RefreshToken); got != http.StatusOK {
		t.Fatalf("confidential revocation HTTP %d", got)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, bob.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("confidential revocation failed: %v", err)
	}
}
func TestExplicitConsentDenyCarriesIssuerAndState(t *testing.T) {
	f := newFixture(t)
	id, csrf := f.ceremony("public.example", "alice")
	resp := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "deny"), "alice")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("consent denial %d", resp.StatusCode)
	}
	target, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || target.Host != "client.example.test" || target.Query().Get("error") != "access_denied" || target.Query().Get("iss") != f.web.URL || target.Query().Get("state") != "opaque-client-state" {
		t.Fatalf("bad consent denial redirect %q err=%v", resp.Header.Get("Location"), err)
	}
	var grants int
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.grants").Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("denied consent created grant: %d %v", grants, err)
	}
	again := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if again.StatusCode != http.StatusBadRequest {
		t.Fatalf("denied transaction became reusable: %d", again.StatusCode)
	}
}
