// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Regression for two independent issuers intentionally sharing one OAuth
// PostgreSQL schema. The same user, client ID, browser session and resource
// make issuer identity (rather than accidental uniqueness) the isolation key.
func TestSharedSchemaIssuerIsolation(t *testing.T) {
	a := newFixture(t)
	ctx := context.Background()
	bNow := a.now
	var handler http.Handler
	bWeb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(bWeb.Close)
	bConfig := a.srv.config
	bConfig.Issuer = bWeb.URL
	bConfig.Now = func() time.Time { return bNow }
	b, err := New(bConfig)
	if err != nil {
		t.Fatal(err)
	}
	handler = b
	if err := Migrate(ctx, a.pool); err != nil {
		t.Fatalf("shared migration must be idempotent: %v", err)
	}
	client := bWeb.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sendB := func(method, path, body string) *http.Response {
		t.Helper()
		var payload io.Reader
		if body != "" {
			payload = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, bWeb.URL+path, payload)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "proof-session", Value: "alice"})
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", bWeb.URL)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	codeB := func(id, csrf string) string {
		t.Helper()
		resp := sendB(http.MethodPost, b.AuthorizePath(), consentForm(id, csrf, "allow"))
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("B consent failed: status=%d", resp.StatusCode)
		}
		uri, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || uri.Query().Get("iss") != bWeb.URL {
			t.Fatalf("B issuer callback mismatch: %v", err)
		}
		return uri.Query().Get("code")
	}
	tokenB := func(code string) TokenResponse {
		t.Helper()
		form := url.Values{"client_id": {"public.example"}, "grant_type": {"authorization_code"},
			"code": {code}, "redirect_uri": {testCallback}, "code_verifier": {testVerifier}, "resource": {testResource}}
		resp := sendB(http.MethodPost, b.TokenPath(), form.Encode())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("B token exchange status=%d", resp.StatusCode)
		}
		var issued TokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
			t.Fatal(err)
		}
		if !validToken(issued.AccessToken) || !validToken(issued.RefreshToken) {
			t.Fatal("B did not issue opaque credentials")
		}
		return issued
	}

	// Both instances see identical session hashes but must retain separate
	// pending one-use ceremonies and allow each client to approve them.
	aID, aCSRF := a.ceremony("public.example", "alice")
	query := url.Values{"response_type": {"code"}, "client_id": {"public.example"},
		"redirect_uri": {testCallback}, "resource": {testResource}, "scope": {"notes.read"},
		"state": {"issuer-b"}, "code_challenge": {testChallenge()}, "code_challenge_method": {"S256"}}
	bResp := sendB(http.MethodGet, b.AuthorizePath()+"?"+query.Encode(), "")
	if bResp.StatusCode != http.StatusOK {
		t.Fatalf("B authorize status=%d", bResp.StatusCode)
	}
	body := mustBody(t, bResp)
	ids, csrf := hiddenID.FindStringSubmatch(body), hiddenCSRF.FindStringSubmatch(body)
	if len(ids) != 2 || len(csrf) != 2 {
		t.Fatal("B missing independent consent transaction")
	}
	var pending int
	if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM oauth.ceremonies").Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("shared browser session overwrote a ceremony: pending=%d err=%v", pending, err)
	}
	aResp := a.mustReq(http.MethodPost, a.srv.AuthorizePath(), consentForm(aID, aCSRF, "allow"), "alice")
	if aResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("A consent replaced by B: %d", aResp.StatusCode)
	}
	aURL, err := url.Parse(aResp.Header.Get("Location"))
	if err != nil || aURL.Query().Get("iss") != a.web.URL {
		t.Fatalf("A callback corrupted: %v", err)
	}
	aIssued := a.tokens("public.example", "", "authorization_code", aURL.Query().Get("code"))
	bIssued := tokenB(codeB(ids[1], csrf[1]))

	aGrant, _, err := a.srv.VerifyAccess(ctx, testResource, aIssued.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	bGrant, _, err := b.VerifyAccess(ctx, testResource, bIssued.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.VerifyAccess(ctx, testResource, aIssued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("B accepted issuer A token: %v", err)
	}
	if _, _, err := a.srv.VerifyAccess(ctx, testResource, bIssued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("A accepted issuer B token: %v", err)
	}
	if err := b.Disconnect(ctx, "alice", aGrant.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("B disconnected issuer A grant: %v", err)
	}
	revoke := url.Values{"client_id": {"public.example"}, "token": {aIssued.RefreshToken}, "token_type_hint": {"refresh_token"}}
	if status := sendB(http.MethodPost, b.RevokePath(), revoke.Encode()).StatusCode; status != http.StatusOK {
		t.Fatalf("B revocation exposed token existence: %d", status)
	}
	if _, _, err := a.srv.VerifyAccess(ctx, testResource, aIssued.AccessToken); err != nil {
		t.Fatalf("B changed A grant state: %v", err)
	}
	// B retains authority over its own grant and does not mutate A.
	if err := b.Disconnect(ctx, "alice", bGrant.ID); err != nil {
		t.Fatalf("B self-disconnect: %v", err)
	}
	if _, _, err := b.VerifyAccess(ctx, testResource, bIssued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("B still accepted explicitly disconnected grant: %v", err)
	}
	// A still has a live credential and a separately pending ceremony. A
	// time-advanced B-only cleanup must not prune either even when their
	// timestamps would qualify under B's clock.
	pendingA, pendingCSRF := a.ceremony("public.example", "alice")
	bNow = bNow.Add(50 * 24 * time.Hour)
	if err := b.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.srv.VerifyAccess(ctx, testResource, aIssued.AccessToken); err != nil {
		t.Fatalf("B pruned A credentials: %v", err)
	}
	aAgain := a.mustReq(http.MethodPost, a.srv.AuthorizePath(), consentForm(pendingA, pendingCSRF, "deny"), "alice")
	if aAgain.StatusCode != http.StatusSeeOther {
		t.Fatalf("B pruned A pending ceremony: %d", aAgain.StatusCode)
	}
	var countA, countB int
	if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM oauth.grants WHERE issuer=$1", a.web.URL).Scan(&countA); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM oauth.grants WHERE issuer=$1", bWeb.URL).Scan(&countB); err != nil {
		t.Fatal(err)
	}
	if countA != 1 || countB != 0 {
		t.Fatalf("issuer-bound grant cleanup failed: A=%d B=%d", countA, countB)
	}
	// B's own revoked grant was eligible for cleanup after fifty days; A's
	// grant and tokens remain despite having older timestamps by B's clock.
}
