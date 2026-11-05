// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Exercise actual upstream exchange validators as the only client credential,
// redirect and PKCE comparators. A failed method/validator must never spend a
// legitimate one-use code or weaken a live confidential client's method.
func TestSDKExchangeSingleAuthorityAndMethodGuards(t *testing.T) {
	f := newFixture(t)
	send := func(endpoint string, v url.Values, basicID, basicSecret, otherAuth string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, f.web.URL+endpoint, strings.NewReader(v.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if basicID != "" {
			req.SetBasicAuth(basicID, basicSecret)
		}
		if otherAuth != "" {
			req.Header.Set("Authorization", otherAuth)
		}
		resp, err := f.browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	codeForm := func(id, code string) url.Values {
		return url.Values{
			"client_id": {id}, "grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {testCallback}, "code_verifier": {testVerifier}, "resource": {testResource},
		}
	}

	publicCode, _ := f.consent("public.example", "alice")
	public := codeForm("public.example", publicCode)
	if got := send(f.srv.TokenPath(), public, "public.example", "not-an-auth-method", ""); got != http.StatusUnauthorized {
		t.Fatalf("Basic method unexpectedly accepted for public client: %d", got)
	}
	if got := send(f.srv.TokenPath(), public, "", "", "Bearer unsupported"); got != http.StatusUnauthorized {
		t.Fatalf("unknown Authorization method unexpectedly accepted: %d", got)
	}
	wrongVerifier := codeForm("public.example", publicCode)
	wrongVerifier.Set("code_verifier", strings.Repeat("x", 43))
	if got := send(f.srv.TokenPath(), wrongVerifier, "", "", ""); got != http.StatusBadRequest {
		t.Fatalf("wrong PKCE verified by AChrix instead of SDK: %d", got)
	}
	if got := send(f.srv.TokenPath(), public, "", "", ""); got != http.StatusOK {
		t.Fatalf("valid SDK public code validation failed after rejects: %d", got)
	}

	confidentialCode, _ := f.consent("confidential.example", "bob")
	confidential := codeForm("confidential.example", confidentialCode)
	secret := strings.Repeat("s", 40)
	if got := send(f.srv.TokenPath(), confidential, "", "", ""); got != http.StatusUnauthorized {
		t.Fatalf("confidential accepted missing Basic: %d", got)
	}
	postSecret := codeForm("confidential.example", confidentialCode)
	postSecret.Set("client_secret", secret)
	if got := send(f.srv.TokenPath(), postSecret, "", "", ""); got != http.StatusBadRequest {
		t.Fatalf("unadvertised client_secret_post accepted: %d", got)
	}
	mismatchedID := codeForm("public.example", confidentialCode)
	if got := send(f.srv.TokenPath(), mismatchedID, "confidential.example", secret, ""); got != http.StatusUnauthorized {
		t.Fatalf("mixed Basic and form client identity accepted: %d", got)
	}
	if got := send(f.srv.TokenPath(), confidential, "confidential.example", secret, ""); got != http.StatusOK {
		t.Fatalf("valid SDK confidential code validation failed after rejects: %d", got)
	}
}
