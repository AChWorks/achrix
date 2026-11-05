// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func parallelTokenCall(f *fixture, grantType, credential string) (int, TokenResponse, error) {
	v := url.Values{"grant_type": {grantType}, "client_id": {"public.example"}, "resource": {testResource}}
	if grantType == "authorization_code" {
		v.Set("code", credential)
		v.Set("code_verifier", testVerifier)
		v.Set("redirect_uri", testCallback)
	} else {
		v.Set("refresh_token", credential)
	}
	req, err := http.NewRequest(http.MethodPost, f.web.URL+f.srv.TokenPath(), strings.NewReader(v.Encode()))
	if err != nil {
		return 0, TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.browser.Do(req)
	if err != nil {
		return 0, TokenResponse{}, err
	}
	defer resp.Body.Close()
	var result TokenResponse
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result)
	}
	return resp.StatusCode, result, err
}
func runConcurrent(t *testing.T, n int, fn func() (int, TokenResponse, error)) (int, TokenResponse) {
	t.Helper()
	statuses := make(chan int, n)
	tokens := make(chan TokenResponse, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-release
			status, token, err := fn()
			if err != nil {
				failures <- err
				return
			}
			statuses <- status
			if status == http.StatusOK {
				tokens <- token
			}
		}()
	}
	close(release)
	wg.Wait()
	close(statuses)
	close(tokens)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	successful := 0
	for status := range statuses {
		if status == http.StatusOK {
			successful++
		} else if status != http.StatusBadRequest {
			t.Errorf("unexpected concurrent exchange status %d", status)
		}
	}
	if successful != 1 {
		t.Fatalf("one-use exchange succeeded %d times, expected one", successful)
	}
	return successful, <-tokens
}
func TestConcurrentCodeAndRefreshRedeemExactlyOnce(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "alice")
	_, first := runConcurrent(t, 8, func() (int, TokenResponse, error) {
		return parallelTokenCall(f, "authorization_code", code)
	})
	if !validToken(first.RefreshToken) {
		t.Fatal("missing first refresh token")
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.access_tokens").Scan(&count); err != nil || count != 1 {
		t.Fatalf("authorization code generated %d access tokens err=%v", count, err)
	}
	_, second := runConcurrent(t, 8, func() (int, TokenResponse, error) {
		return parallelTokenCall(f, "refresh_token", first.RefreshToken)
	})
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, second.AccessToken); err != ErrInvalidGrant {
		t.Fatalf("refresh replay did not revoke family: %v", err)
	}
	var revoked bool
	if err := f.pool.QueryRow(context.Background(), "SELECT revoked_at IS NOT NULL FROM oauth.grants LIMIT 1").Scan(&revoked); err != nil || !revoked {
		t.Fatalf("refresh replay did not persist revocation: revoked=%v err=%v", revoked, err)
	}
	// A newer token cannot reanimate the replayed family.
	status, _, err := parallelTokenCall(f, "refresh_token", second.RefreshToken)
	if err != nil || status != http.StatusBadRequest {
		t.Fatalf("rotated family unexpectedly accepted %d, err=%v", status, err)
	}
}
func TestInvalidCodeAndRefreshDoNotWidenScope(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "bob")
	for _, tt := range []struct{ field, value string }{
		{"code_verifier", "wrong"},
		{"redirect_uri", "https://attacker.test/callback"},
		{"resource", "https://other.test/api"},
	} {
		values := url.Values{"grant_type": {"authorization_code"}, "client_id": {"public.example"}, "resource": {testResource}, "code": {code}, "redirect_uri": {testCallback}, "code_verifier": {testVerifier}}
		values.Set(tt.field, tt.value)
		resp := f.mustReq(http.MethodPost, f.srv.TokenPath(), values.Encode(), "")
		if resp.StatusCode == http.StatusOK {
			t.Fatal("invalid code binding accepted:", tt.field)
		}
	}
	legitimate := f.tokens("public.example", "", "authorization_code", code)
	if result := f.tokenCall("public.example", "", "refresh_token", legitimate.RefreshToken); result.StatusCode != http.StatusOK {
		t.Fatalf("valid refresh token invalid after rejected requests: %d", result.StatusCode)
	}
	var scopes []string
	err := f.pool.QueryRow(context.Background(), "SELECT scopes FROM oauth.grants LIMIT 1").Scan(&scopes)
	if err != nil || joinScopes(scopes) != "notes.read" {
		t.Fatalf("scope widened: %v err=%v", scopes, err)
	}
}
