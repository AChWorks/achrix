// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPolicyRevokedBeforeConsentCommitsDenialAndConsumesTransaction(t *testing.T) {
	f := newFixture(t)
	id, csrf := f.ceremony("public.example", "alice")
	f.policyRevoked.Store(true)
	denied := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if denied.StatusCode != http.StatusSeeOther {
		t.Fatalf("invalid consent policy denial status: %d", denied.StatusCode)
	}
	target, err := url.Parse(denied.Header.Get("Location"))
	if err != nil || target.Host != "client.example.test" || target.Query().Get("error") != "access_denied" ||
		target.Query().Get("iss") != f.web.URL || target.Query().Get("state") != "opaque-client-state" {
		t.Fatalf("policy denial must use validated RFC9207 redirect: %q err=%v", denied.Header.Get("Location"), err)
	}
	f.policyRevoked.Store(false)
	replay := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("policy-denied consent remained reusable: %d", replay.StatusCode)
	}
	var pending, grants int
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.ceremonies").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.grants").Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if pending != 0 || grants != 0 {
		t.Fatalf("denied consent retained pending=%d or grant=%d", pending, grants)
	}
}

func TestLiveClientDisableScopeShrinkAndRegistryOutageAcrossEndpoints(t *testing.T) {
	f := newFixture(t)
	u, _ := url.Parse(authURL(f, "public.example"))
	q := u.Query()
	q.Set("scope", "notes.write")
	consent := f.mustReq(http.MethodGet, f.srv.AuthorizePath()+"?"+q.Encode(), "", "alice")
	if consent.StatusCode != http.StatusOK {
		t.Fatalf("pre-revocation consent missing: %d", consent.StatusCode)
	}
	body := mustBody(t, consent)
	ids, cs := hiddenID.FindStringSubmatch(body), hiddenCSRF.FindStringSubmatch(body)
	if len(ids) != 2 || len(cs) != 2 {
		t.Fatal("missing one-use consent")
	}
	approved := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(ids[1], cs[1], "allow"), "alice")
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approval status %d", approved.StatusCode)
	}
	callback, err := url.Parse(approved.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := callback.Query().Get("code")
	if !validToken(code) {
		t.Fatal("missing code")
	}
	reqToken := func(grant, credential string) (int, TokenResponse) {
		vals := url.Values{"grant_type": {grant}, "resource": {testResource}, "client_id": {"public.example"}}
		if grant == "authorization_code" {
			vals.Set("code", credential)
			vals.Set("code_verifier", testVerifier)
			vals.Set("redirect_uri", testCallback)
		} else {
			vals.Set("refresh_token", credential)
		}
		resp := f.mustReq(http.MethodPost, f.srv.TokenPath(), vals.Encode(), "")
		var tr TokenResponse
		if resp.StatusCode == 200 {
			if err := json.Unmarshal([]byte(mustBody(t, resp)), &tr); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, tr
	}
	status, issued := reqToken("authorization_code", code)
	if status != http.StatusOK {
		t.Fatalf("granted code exchange %d", status)
	}
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); err != nil {
		t.Fatal(err)
	}
	f.narrowClient.Store(true)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("scope shrink not live on access: %v", err)
	}
	if status, _ = reqToken("refresh_token", issued.RefreshToken); status != http.StatusBadRequest {
		t.Fatalf("scope shrink not live on refresh: %d", status)
	}
	f.narrowClient.Store(false)
	f.clientDisabled.Store(true)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("disabled client access accepted: %v", err)
	}
	if status, _ = reqToken("refresh_token", issued.RefreshToken); status != http.StatusUnauthorized {
		t.Fatalf("disabled client refresh accepted: %d", status)
	}
	rejected := f.mustReq(http.MethodGet, authURL(f, "public.example"), "", "alice")
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("disabled client authorize accepted: %d", rejected.StatusCode)
	}
	f.clientDisabled.Store(false)
	f.clientFailure.Store(true)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("client registry outage silently accepted: %v", err)
	}
	if status, _ = reqToken("refresh_token", issued.RefreshToken); status != http.StatusServiceUnavailable {
		t.Fatalf("unavailable registry lost failure semantics on token: %d", status)
	}
	unavailable := f.mustReq(http.MethodGet, authURL(f, "public.example"), "", "alice")
	if unavailable.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable registry lost failure semantics on authorize: %d", unavailable.StatusCode)
	}
	revoke := url.Values{"client_id": {"public.example"}, "token": {issued.RefreshToken}}
	onRevoke := f.mustReq(http.MethodPost, f.srv.RevokePath(), revoke.Encode(), "")
	if onRevoke.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable registry lost failure semantics on revoke: %d", onRevoke.StatusCode)
	}
	f.clientFailure.Store(false)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); err != nil {
		t.Fatalf("recovered registry must not implicitly revoke preexisting grant: %v", err)
	}
}

func TestRFC6749BasicEncodedSpecialSecretCodeRefreshAndRevoke(t *testing.T) {
	f := newFixture(t)
	const id = "confidential.reserved"
	secret := strings.Repeat("C%:+&/", 7)
	code, _ := f.consent(id, "bob")
	send := func(endpoint string, form url.Values, username, pass string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, f.web.URL+endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(username, pass)
		return f.browser.Do(req)
	}
	codeForm := url.Values{"grant_type": {"authorization_code"}, "resource": {testResource}, "code": {code}, "redirect_uri": {testCallback}, "code_verifier": {testVerifier}}
	bad, err := send(f.srv.TokenPath(), codeForm, id, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw reserved Basic secret unexpectedly accepted: %d", bad.StatusCode)
	}
	good, err := send(f.srv.TokenPath(), codeForm, url.QueryEscape(id), url.QueryEscape(secret))
	if err != nil {
		t.Fatal(err)
	}
	defer good.Body.Close()
	if good.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(good.Body, 300))
		t.Fatalf("standards-compliant Basic code failed: %d %s", good.StatusCode, raw)
	}
	var initial TokenResponse
	if err := json.NewDecoder(good.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	if !validToken(initial.RefreshToken) {
		t.Fatal("missing refresh")
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "resource": {testResource}, "refresh_token": {initial.RefreshToken}}
	refresh, err := send(f.srv.TokenPath(), refreshForm, url.QueryEscape(id), url.QueryEscape(secret))
	if err != nil {
		t.Fatal(err)
	}
	defer refresh.Body.Close()
	if refresh.StatusCode != http.StatusOK {
		t.Fatalf("standards-compliant Basic refresh failed: %d", refresh.StatusCode)
	}
	var rotated TokenResponse
	if err := json.NewDecoder(refresh.Body).Decode(&rotated); err != nil {
		t.Fatal(err)
	}
	revokeForm := url.Values{"token": {rotated.RefreshToken}, "token_type_hint": {"refresh_token"}}
	revoke, err := send(f.srv.RevokePath(), revokeForm, url.QueryEscape(id), url.QueryEscape(secret))
	if err != nil {
		t.Fatal(err)
	}
	defer revoke.Body.Close()
	if revoke.StatusCode != http.StatusOK {
		t.Fatalf("standards-compliant Basic revoke failed: %d", revoke.StatusCode)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, rotated.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("revocation failed to invalidate token family: %v", err)
	}
}
