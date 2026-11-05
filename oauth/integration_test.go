// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var hiddenID = regexp.MustCompile("name='transaction' value='([^']+)'")
var hiddenCSRF = regexp.MustCompile("name='csrf' value='([^']+)'")

const testVerifier = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type fixture struct {
	t              *testing.T
	srv            *Server
	web            *httptest.Server
	browser        *http.Client
	pool           *pgxpool.Pool
	deny           atomic.Bool
	disabled       atomic.Bool
	failure        atomic.Bool
	clientDisabled atomic.Bool
	clientFailure  atomic.Bool
	narrowClient   atomic.Bool
	policyRevoked  atomic.Bool
	now            time.Time
	mu             sync.Mutex
	errors         []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("ACHRIX_OAUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_OAUTH_TEST_DSN absent: PostgreSQL OAuth tests not run")
	}
	cfg := taskTestConfig(t, dsn, "achrix_oauth_test")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	checkTaskTestDatabase(t, ctx, pool, "achrix_oauth_test")
	if _, err = pool.Exec(ctx, "DROP SCHEMA IF EXISTS oauth CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal("idempotent migration:", err)
	}
	f := &fixture{t: t, pool: pool, now: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	var handler http.Handler
	f.web = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.web.Close)
	f.browser = f.web.Client()
	f.browser.CheckRedirect = func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	conf := Config{
		Issuer: f.web.URL, Resource: testResource, Pool: pool,
		Clients: []Client{
			{ID: "public.example", Name: "Notes Client", Enabled: true, RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read", "notes.write"}},
			{ID: "confidential.example", Enabled: true, Confidential: true, SecretDigest: SecretDigest(strings.Repeat("s", 40)), RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read", "notes.write"}},
			{ID: "confidential.reserved", Enabled: true, Confidential: true, SecretDigest: SecretDigest(strings.Repeat("C%:+&/", 7)), RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read"}},
		},
		ClientLookup: func(ctx context.Context, id string) (Client, error) {
			if f.clientFailure.Load() {
				return Client{}, errors.New("private client directory unavailable")
			}
			var c Client
			switch id {
			case "public.example":
				c = Client{ID: id, Name: "Notes Client", Enabled: true, RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read", "notes.write"}}
				if f.clientDisabled.Load() {
					c.Enabled = false
				}
				if f.narrowClient.Load() {
					c.Scopes = []string{"notes.read"}
				}
			case "confidential.example":
				c = Client{ID: id, Confidential: true, Enabled: true, SecretDigest: SecretDigest(strings.Repeat("s", 40)), RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read", "notes.write"}}
			case "confidential.reserved":
				c = Client{ID: id, Confidential: true, Enabled: true, SecretDigest: SecretDigest(strings.Repeat("C%:+&/", 7)), RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read"}}
			default:
				return Client{}, ErrInvalidGrant
			}
			return c, nil
		},
		Session: func(ctx context.Context, r *http.Request) (Session, error) {
			cookie, err := r.Cookie("proof-session")
			if err != nil || cookie.Value != "alice" && cookie.Value != "bob" {
				return Session{}, ErrInvalidGrant
			}
			return Session{Subject: cookie.Value, Binding: sha256.Sum256([]byte("session-proof-" + cookie.Value))}, nil
		},
		Permit: func(ctx context.Context, g Grant) error {
			if f.failure.Load() {
				return errors.New("private permit database unavailable")
			}
			if f.policyRevoked.Load() || f.disabled.Load() && g.Subject == "alice" || f.deny.Load() && strings.Contains(joinScopes(g.Scopes), "notes.write") {
				return ErrInvalidGrant
			}
			return nil
		},
		RenderConsent: testRenderConsent,
		Diagnostic: func(ctx context.Context, phase, status string, err error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.errors = append(f.errors, phase+":"+status)
		},
		Now: func() time.Time { return f.now },
	}
	f.srv, err = New(conf)
	if err != nil {
		t.Fatal(err)
	}
	handler = f.srv
	return f
}
func (f *fixture) req(method, path, body, user string) (*http.Response, error) {
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.web.URL+path, payload)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.AddCookie(&http.Cookie{Name: "proof-session", Value: user})
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", f.web.URL)
	}
	return f.browser.Do(req)
}
func (f *fixture) mustReq(method, path, body, user string) *http.Response {
	f.t.Helper()
	resp, err := f.req(method, path, body, user)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func mustBody(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func authURL(f *fixture, client string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {testCallback}, "resource": {testResource}, "scope": {"notes.read"}, "state": {"opaque-client-state"}, "code_challenge": {testChallenge()}, "code_challenge_method": {"S256"}}
	return f.srv.AuthorizePath() + "?" + q.Encode()
}
func (f *fixture) ceremony(client, user string) (string, string) {
	f.t.Helper()
	resp := f.mustReq(http.MethodGet, authURL(f, client), "", user)
	body := mustBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("authorize status %d %s", resp.StatusCode, body)
	}
	ids, cs := hiddenID.FindStringSubmatch(body), hiddenCSRF.FindStringSubmatch(body)
	if len(ids) != 2 || len(cs) != 2 {
		f.t.Fatalf("missing transaction/CSRF from consent response: %s", body)
	}
	return ids[1], cs[1]
}
func consentForm(id, csrf, decision string) string {
	return url.Values{"transaction": {id}, "csrf": {csrf}, "decision": {decision}}.Encode()
}
func (f *fixture) consent(client, user string) (string, *http.Response) {
	f.t.Helper()
	id, csrf := f.ceremony(client, user)
	resp := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), user)
	if resp.StatusCode != http.StatusSeeOther {
		f.t.Fatalf("consent HTTP %d: %s", resp.StatusCode, mustBody(f.t, resp))
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "client.example.test" || u.Path != "/callback" || u.Query().Get("state") != "opaque-client-state" || u.Query().Get("iss") != f.web.URL {
		f.t.Fatalf("issuer/redirect/state mismatch: %s", u)
	}
	code := u.Query().Get("code")
	if !validToken(code) {
		f.t.Fatal("missing opaque authorization code")
	}
	return code, resp
}
func (f *fixture) tokenCall(client, secret, grantType, value string) *http.Response {
	f.t.Helper()
	v := url.Values{"grant_type": {grantType}, "resource": {testResource}}
	if grantType == "authorization_code" {
		v.Set("code", value)
		v.Set("redirect_uri", testCallback)
		v.Set("code_verifier", testVerifier)
	} else {
		v.Set("refresh_token", value)
	}
	if secret == "" {
		v.Set("client_id", client)
	}
	req, err := http.NewRequest(http.MethodPost, f.web.URL+f.srv.TokenPath(), strings.NewReader(v.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if secret != "" {
		req.SetBasicAuth(client, secret)
	}
	resp, err := f.browser.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func (f *fixture) tokens(client, secret, grantType, value string) TokenResponse {
	f.t.Helper()
	resp := f.tokenCall(client, secret, grantType, value)
	raw := mustBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("%s token response %d %s", grantType, resp.StatusCode, raw)
	}
	var result TokenResponse
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		f.t.Fatal(err)
	}
	if !validToken(result.AccessToken) || !validToken(result.RefreshToken) || result.TokenType != "Bearer" || result.ExpiresIn != 600 || result.Scope != "notes.read" || result.AccessToken == result.RefreshToken {
		f.t.Fatalf("bad issued credentials (secret values withheld), token type=%q expiry=%d scope=%q", result.TokenType, result.ExpiresIn, result.Scope)
	}
	return result
}
func TestPostgresRealAuthorizationCodeAndTokenLifecycle(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "alice")
	issued := f.tokens("public.example", "", "authorization_code", code)
	g, expiry, err := f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken)
	if err != nil || g.Subject != "alice" || g.ClientID != "public.example" || g.Resource != testResource || !expiry.Equal(f.now.Add(10*time.Minute)) {
		t.Fatalf("verified grant binding invalid: subject=%s client=%s resource=%s err=%v", g.Subject, g.ClientID, g.Resource, err)
	}
	var stored []byte
	if err = f.pool.QueryRow(context.Background(), "SELECT token_hash FROM oauth.access_tokens LIMIT 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || string(stored) == issued.AccessToken {
		t.Fatal("plaintext bearer persisted")
	}
	if resp := f.tokenCall("public.example", "", "authorization_code", code); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("code replay status %d", resp.StatusCode)
	}
	refreshed := f.tokens("public.example", "", "refresh_token", issued.RefreshToken)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, refreshed.AccessToken); err != nil {
		t.Fatal(err)
	}
	// Logged-out browser no longer supplies a session, but existing independent
	// authorization remains valid until expiry, revocation, disablement or replay.
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); err != nil {
		t.Fatal("logout must not implicitly revoke existing grant:", err)
	}
	f.disabled.Store(true)
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("disabled product user accepted: %v", err)
	}
	f.disabled.Store(false)
	if err = f.srv.Disconnect(context.Background(), "bob", g.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("cross-user disconnect accepted: %v", err)
	}
	if err = f.srv.Disconnect(context.Background(), "alice", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.srv.VerifyAccess(context.Background(), testResource, refreshed.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("disconnect did not revoke family: %v", err)
	}
	if resp := f.tokenCall("public.example", "", "refresh_token", refreshed.RefreshToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("revoked refresh status %d", resp.StatusCode)
	}
	if err = Migrate(context.Background(), f.pool); err != nil {
		t.Fatal("migration retry:", err)
	}
}
func TestConsentCSRFSessionAndRedirectDenials(t *testing.T) {
	f := newFixture(t)
	id, csrf := f.ceremony("public.example", "alice")
	for _, tc := range []struct {
		name, user, origin, csrf string
		status                   int
	}{
		{"other user", "bob", f.web.URL, csrf, http.StatusForbidden},
		{"bad csrf", "alice", f.web.URL, "bad", http.StatusBadRequest},
		{"foreign origin", "alice", "https://attacker.test", csrf, http.StatusForbidden},
		{"not logged in", "", f.web.URL, csrf, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, f.web.URL+f.srv.AuthorizePath(), strings.NewReader(consentForm(id, tc.csrf, "allow")))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tc.origin)
			if tc.user != "" {
				req.AddCookie(&http.Cookie{Name: "proof-session", Value: tc.user})
			}
			resp, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("consent refusal %d want %d", resp.StatusCode, tc.status)
			}
		})
	}
	ok := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatalf("original session cannot approve: %d", ok.StatusCode)
	}
	replay := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("consent replay status %d", replay.StatusCode)
	}

	// Once an EXACT pre-registered redirect is identified, RFC 9207 requires
	// the canonical issuer in both successful and error redirect responses.
	for _, tc := range []struct {
		name           string
		edit           func(url.Values)
		expectedStatus int
		expectedError  string
	}{
		{"wrong redirect", func(v url.Values) { v.Set("redirect_uri", "https://attacker.test/callback") }, http.StatusBadRequest, ""},
		{"wrong resource", func(v url.Values) { v.Set("resource", "https://sibling.example.test/mcp") }, http.StatusSeeOther, "invalid_target"},
		{"plain PKCE", func(v url.Values) { v.Set("code_challenge_method", "plain") }, http.StatusSeeOther, "invalid_request"},
		{"missing PKCE", func(v url.Values) { v.Del("code_challenge") }, http.StatusSeeOther, "invalid_request"},
		{"wrong response type", func(v url.Values) { v.Set("response_type", "token") }, http.StatusSeeOther, "unsupported_response_type"},
		{"unapproved scope", func(v url.Values) { v.Set("scope", "admin.root") }, http.StatusSeeOther, "invalid_scope"},
		{"duplicate client", func(v url.Values) { v.Add("client_id", "public.example") }, http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(authURL(f, "public.example"))
			if err != nil {
				t.Fatal(err)
			}
			v := u.Query()
			tc.edit(v)
			resp := f.mustReq(http.MethodGet, f.srv.AuthorizePath()+"?"+v.Encode(), "", "alice")
			if resp.StatusCode != tc.expectedStatus {
				t.Fatalf("error status %d want %d", resp.StatusCode, tc.expectedStatus)
			}
			location := resp.Header.Get("Location")
			if tc.expectedError == "" {
				if location != "" {
					t.Fatal("redirect to unregistered or ambiguous client")
				}
				return
			}
			target, err := url.Parse(location)
			if err != nil || target.Host != "client.example.test" || target.Path != "/callback" || target.Query().Get("error") != tc.expectedError || target.Query().Get("iss") != f.web.URL || target.Query().Get("state") != "opaque-client-state" {
				t.Fatalf("invalid RFC 9207 error redirect: %q err=%v", location, err)
			}
		})
	}

}
func TestConfidentialClientAuthenticationAndRefreshReplay(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("confidential.example", "bob")
	for _, tc := range []struct{ name, client, secret string }{
		{"public downgrade", "confidential.example", ""},
		{"wrong secret", "confidential.example", strings.Repeat("x", 40)},
		{"different client", "public.example", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := f.tokenCall(tc.client, tc.secret, "authorization_code", code)
			if resp.StatusCode == http.StatusOK {
				t.Fatal("authorization code accepted without exact confidential client")
			}
		})
	}
	secret := strings.Repeat("s", 40)
	issued := f.tokens("confidential.example", secret, "authorization_code", code)
	if resp := f.tokenCall("confidential.example", strings.Repeat("x", 40), "refresh_token", issued.RefreshToken); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret status %d", resp.StatusCode)
	}
	next := f.tokens("confidential.example", secret, "refresh_token", issued.RefreshToken)
	if resp := f.tokenCall("confidential.example", secret, "refresh_token", issued.RefreshToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("refresh replay returned %d", resp.StatusCode)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, next.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("replay family still valid: %v", err)
	}
}
func TestTwoUserFamilyIsolationAndCurrentRights(t *testing.T) {
	f := newFixture(t)
	first, _ := f.consent("public.example", "alice")
	a := f.tokens("public.example", "", "authorization_code", first)
	second, _ := f.consent("public.example", "bob")
	b := f.tokens("public.example", "", "authorization_code", second)
	if _, _, err := f.srv.VerifyAccess(context.Background(), "https://different.example.test/mcp", a.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("sibling resource accepted: %v", err)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, b.AccessToken); err != nil {
		t.Fatal(err)
	}
	if resp := f.tokenCall("public.example", "", "refresh_token", a.RefreshToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("alice refresh %d", resp.StatusCode)
	}
	// Alice's replay cannot revoke Bob's separate grant family.
	if resp := f.tokenCall("public.example", "", "refresh_token", a.RefreshToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("replay %d", resp.StatusCode)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, b.AccessToken); err != nil {
		t.Fatal("unrelated user grant lost:", err)
	}
}
func TestServerRejectsForgedTlsHostAndUnsupportedGrant(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("GET", f.web.URL+f.srv.MetadataPath(), nil)
	req.URL.Host = ""
	req.URL.Scheme = ""
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("X-Forwarded-Host", "attacker.test")
	req.Host = "attacker.test"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatal("forged host accepted")
	}
	bad := f.mustReq(http.MethodPost, f.srv.TokenPath(), url.Values{"grant_type": {"password"}, "client_id": {"public.example"}, "resource": {testResource}}.Encode(), "")
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatal("password grant accepted")
	}
}

func TestConsentLatestTransactionAndCorrelation(t *testing.T) {
	f := newFixture(t)
	firstID, firstCSRF := f.ceremony("public.example", "alice")
	secondID, secondCSRF := f.ceremony("public.example", "alice")
	if firstID == secondID || firstCSRF == secondCSRF {
		t.Fatal("reused CSRF or consent transaction")
	}
	old := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(firstID, firstCSRF, "allow"), "alice")
	if old.StatusCode != http.StatusBadRequest {
		t.Fatalf("prior consent accepted: %d", old.StatusCode)
	}
	latest := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(secondID, secondCSRF, "allow"), "alice")
	if latest.StatusCode != http.StatusSeeOther {
		t.Fatalf("new consent invalid: %d", latest.StatusCode)
	}
	id := latest.Header.Get("X-Request-ID")
	if !validToken(id) {
		t.Fatalf("missing safe correlation id, length=%d", len(id))
	}
	unknown := f.mustReq(http.MethodGet, "/invalid-path", "", "")
	if !validToken(unknown.Header.Get("X-Request-ID")) || unknown.Header.Get("X-Request-ID") == id {
		t.Fatal("correlation must be unique even on unknown endpoints")
	}
}

func TestProductPermissionOutageFailsClosedAndKeepsDiagnostic(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "alice")
	issued := f.tokens("public.example", "", "authorization_code", code)
	f.failure.Store(true)
	resp := f.mustReq(http.MethodGet, authURL(f, "public.example"), "", "alice")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("policy dependency outage yielded %d, want 503", resp.StatusCode)
	}
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("access verified with broken permission dependency: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errors) == 0 {
		t.Fatal("policy failure diagnostic not preserved")
	}
	f.failure.Store(false)
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, issued.AccessToken); err != nil {
		t.Fatal("recovered dependency left an otherwise-valid grant unusable:", err)
	}
}
