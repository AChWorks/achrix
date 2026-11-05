// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

// A genuine maintained Go OAuth2 client exchanges a PKCE code over HTTPS and
// invokes an existing, PostgreSQL-backed Identity application operation
// through the real Bridge resource-server boundary. This does NOT claim a
// ChatGPT, Claude or end-user product UI onboarding proof.
func TestRealIdentityOauth2ClientBridgeAndLogout(t *testing.T) {
	dsn := os.Getenv("ACHRIX_OAUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_OAUTH_TEST_DSN absent")
	}
	pgConfig := taskTestConfig(t, dsn, "achrix_oauth_test")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pgConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	checkTaskTestDatabase(t, ctx, pool, "achrix_oauth_test")
	// All tests are intentionally sequential: each controls this private schema.
	for _, schema := range []string{"oauth", "identity", "audit"} {
		if _, err = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = audit.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err = identity.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	am, err := audit.NewPostgres(dsn, audit.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	im, err := identity.NewPostgres(dsn, identity.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	var forbidAlice atomic.Bool
	var aliceID string
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, capability, target string) error {
		if p == identity.PublicPrincipal && capability == identity.Authentication && target == "" {
			return nil
		}
		if p == "owner" {
			if capability == identity.AccountCreate || capability == identity.AccountRead || capability == identity.AccountSetEnabled || capability == audit.Append {
				return nil
			}
		}
		if capability == identity.AccountRead && target == string(p) && p != "" {
			if forbidAlice.Load() && string(p) == aliceID {
				return achrix.ErrDenied
			}
			return nil
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, am, im)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(c); err != nil {
			t.Error(err)
		}
	})
	auditS, err := audit.NewService(app, am)
	if err != nil {
		t.Fatal(err)
	}
	idSvc, err := identity.NewService(app, im, auditS)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := idSvc.CreateAccount(ctx, "owner", "alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	aliceID = alice.ID
	bob, err := idSvc.CreateAccount(ctx, "owner", "bob", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	aliceSession, err := idSvc.Login(ctx, "alice", "correct horse battery staple", "")
	if err != nil {
		t.Fatal(err)
	}
	bobSession, err := idSvc.Login(ctx, "bob", "correct horse battery staple", "")
	if err != nil {
		t.Fatal(err)
	}

	// Two independent TLS endpoints, one product-owned resource and one optional
	// issuer. No synthetic standalone bearer credential can impersonate Identity.
	var resourceHandler http.Handler
	resourceHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { resourceHandler.ServeHTTP(w, r) }))
	t.Cleanup(resourceHTTP.Close)
	resource := resourceHTTP.URL + "/api"
	var oauthHandler http.Handler
	issuerHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oauthHandler.ServeHTTP(w, r) }))
	t.Cleanup(issuerHTTP.Close)

	sessionCallback := func(ctx context.Context, r *http.Request) (Session, error) {
		var value string
		count := 0
		for _, cookie := range r.Cookies() {
			if cookie.Name == identity.CookieName {
				count++
				value = cookie.Value
			}
		}
		if count != 1 || value == "" {
			return Session{}, identity.ErrAuthentication
		}
		principal, err := idSvc.Authenticate(ctx, value)
		if err != nil {
			return Session{}, err
		}
		return Session{Subject: string(principal), Binding: sha256.Sum256([]byte(value))}, nil
	}
	permit := func(ctx context.Context, g Grant) error {
		account, err := idSvc.Account(ctx, "owner", g.Subject)
		if err != nil {
			return err
		}
		if !account.Enabled {
			return ErrInvalidGrant
		}
		return app.Authorize(ctx, achrix.Principal(g.Subject), identity.AccountRead, g.Subject)
	}
	issuer, err := New(Config{
		Issuer: issuerHTTP.URL, Resource: resource, Pool: pool,
		Clients: []Client{{ID: "real-go-oauth2", Name: "Go OAuth2 Client", RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read"}, Enabled: true}},
		ClientLookup: func(_ context.Context, id string) (Client, error) {
			if id != "real-go-oauth2" {
				return Client{}, ErrInvalidGrant
			}
			return Client{ID: "real-go-oauth2", Name: "Go OAuth2 Client", RedirectURIs: []string{testCallback}, Scopes: []string{"notes.read"}, Enabled: true}, nil
		},
		Session: sessionCallback, Permit: permit, RenderConsent: testRenderConsent,
		Diagnostic: func(context.Context, string, string, error) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthHandler = issuer

	route, err := bridge.JSONHandler[struct{}, identity.Account]("identity.account.read",
		func(r *http.Request) (struct{}, error) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/self" || r.URL.RawQuery != "" {
				return struct{}{}, errors.New("invalid request")
			}
			return struct{}{}, nil
		},
		func(ctx context.Context, _ struct{}) (identity.Account, error) {
			verified, ok := bridge.RequestFromContext(ctx)
			if !ok {
				return identity.Account{}, errors.New("missing verified Bridge context")
			}
			return idSvc.Account(ctx, verified.Caller.Principal, string(verified.Caller.Principal))
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ops := http.NewServeMux()
	ops.Handle("GET /api/self", route)
	endpoint, err := bridge.NewJSONHTTP(bridge.Config{
		ResourceURL: resource, AuthorizationServers: []string{issuerHTTP.URL}, Scopes: []string{"notes.read"},
		Verify: func(ctx context.Context, raw string) (bridge.Credential, error) {
			grant, expiry, err := issuer.VerifyAccess(ctx, resource, raw)
			if errors.Is(err, ErrInvalidGrant) {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			if err != nil {
				return bridge.Credential{}, err
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: achrix.Principal(grant.Subject), ClientID: grant.ClientID}, ExpiresAt: expiry, Scopes: grant.Scopes}, nil
		},
		Admit:      func(context.Context, bridge.Caller) error { return nil },
		Diagnostic: func(context.Context, bridge.Diagnostic) {},
	}, ops)
	if err != nil {
		t.Fatal(err)
	}
	resourceMux := http.NewServeMux()
	resourceMux.Handle("/.well-known/oauth-protected-resource/api", endpoint.MetadataHandler())
	resourceMux.Handle("/", endpoint)
	resourceHandler = resourceMux

	clientBrowser := issuerHTTP.Client()
	clientBrowser.CheckRedirect = func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	clientBrowser.Timeout = 4 * time.Second
	issuerDocument, err := clientBrowser.Get(issuerHTTP.URL + issuer.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	var authorizationMetadata struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err = json.NewDecoder(io.LimitReader(issuerDocument.Body, 16384)).Decode(&authorizationMetadata); err != nil {
		issuerDocument.Body.Close()
		t.Fatal(err)
	}
	issuerDocument.Body.Close()
	if issuerDocument.StatusCode != http.StatusOK || authorizationMetadata.Issuer != issuerHTTP.URL ||
		authorizationMetadata.AuthorizationEndpoint != issuerHTTP.URL+"/authorize" ||
		authorizationMetadata.TokenEndpoint != issuerHTTP.URL+"/token" {
		t.Fatal("actual issuer metadata does not match canonical live endpoints")
	}
	resourceDocument, err := resourceHTTP.Client().Get(resourceHTTP.URL + "/.well-known/oauth-protected-resource/api")
	if err != nil {
		t.Fatal(err)
	}
	var protectedMetadata struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	if err = json.NewDecoder(io.LimitReader(resourceDocument.Body, 16384)).Decode(&protectedMetadata); err != nil {
		resourceDocument.Body.Close()
		t.Fatal(err)
	}
	resourceDocument.Body.Close()
	if resourceDocument.StatusCode != http.StatusOK || protectedMetadata.Resource != resource ||
		len(protectedMetadata.AuthorizationServers) != 1 || protectedMetadata.AuthorizationServers[0] != authorizationMetadata.Issuer ||
		len(protectedMetadata.Scopes) != 1 || protectedMetadata.Scopes[0] != "notes.read" {
		t.Fatalf("Bridge resource metadata does not identify the exact real issuer and audience: resource=%s issuer=%v scopes=%v",
			protectedMetadata.Resource, protectedMetadata.AuthorizationServers, protectedMetadata.Scopes)
	}
	uncredentialed, err := resourceHTTP.Client().Get(resource + "/self")
	if err != nil {
		t.Fatal(err)
	}
	uncredentialed.Body.Close()
	challenge := uncredentialed.Header.Get("WWW-Authenticate")
	if uncredentialed.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(challenge, `resource_metadata="`+resourceHTTP.URL+"/.well-known/oauth-protected-resource/api"+`"`) ||
		strings.Contains(challenge, "attacker") {
		t.Fatalf("missing correct protected resource discovery challenge: status=%d challenge=%q", uncredentialed.StatusCode, challenge)
	}
	library := oauth2.Config{ClientID: "real-go-oauth2", RedirectURL: testCallback, Scopes: []string{"notes.read"}, Endpoint: oauth2.Endpoint{AuthURL: issuerHTTP.URL + "/authorize", TokenURL: issuerHTTP.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	clientContext := context.WithValue(context.Background(), oauth2.HTTPClient, clientBrowser)
	authorize := func(sessionToken, state string) (*http.Response, error) {
		u := library.AuthCodeURL(state, oauth2.S256ChallengeOption(testVerifier), oauth2.SetAuthURLParam("resource", resource))
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if sessionToken != "" {
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: sessionToken})
		}
		return clientBrowser.Do(req)
	}
	finish := func(sessionToken string, begin *http.Response) string {
		t.Helper()
		if begin.StatusCode != http.StatusOK {
			t.Fatalf("authorize status %d", begin.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(begin.Body, 32<<10))
		begin.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		m, n := hiddenID.FindSubmatch(raw), hiddenCSRF.FindSubmatch(raw)
		if len(m) != 2 || len(n) != 2 {
			t.Fatal("missing consent transaction")
		}
		payload := url.Values{"transaction": {string(m[1])}, "csrf": {string(n[1])}, "decision": {"allow"}}.Encode()
		req, err := http.NewRequest(http.MethodPost, issuerHTTP.URL+"/authorize", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", issuerHTTP.URL)
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: sessionToken})
		response, err := clientBrowser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("consent status %d", response.StatusCode)
		}
		callback, err := url.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if callback.Query().Get("iss") != issuerHTTP.URL || callback.Query().Get("state") != "state-a" {
			t.Fatal("client state/issuer mismatch")
		}
		return callback.Query().Get("code")
	}
	begin, err := authorize(aliceSession.Token, "state-a")
	if err != nil {
		t.Fatal(err)
	}
	code := finish(aliceSession.Token, begin)
	tokenA, err := library.Exchange(clientContext, code, oauth2.VerifierOption(testVerifier), oauth2.SetAuthURLParam("resource", resource))
	if err != nil {
		t.Fatal("real oauth2 client exchange:", err)
	}
	if !validToken(tokenA.AccessToken) || tokenA.RefreshToken == "" {
		t.Fatal("real oauth2 client did not receive credential")
	}
	read := func(tokenValue string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, resource+"/self", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tokenValue)
		response, err := resourceHTTP.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 8192))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode == http.StatusOK {
			var account identity.Account
			if err = json.Unmarshal(body, &account); err != nil {
				t.Fatal(err)
			}
			return response.StatusCode, account.ID
		}
		return response.StatusCode, ""
	}
	if status, subject := read(tokenA.AccessToken); status != 200 || subject != alice.ID {
		t.Fatalf("verified Alice operation status=%d subject=%s", status, subject)
	}
	// An independent real user logs in to the same registered OAuth client.
	begin, err = authorize(bobSession.Token, "state-a")
	if err != nil {
		t.Fatal(err)
	}
	code = finish(bobSession.Token, begin)
	tokenB, err := library.Exchange(clientContext, code, oauth2.VerifierOption(testVerifier), oauth2.SetAuthURLParam("resource", resource))
	if err != nil {
		t.Fatal(err)
	}
	if status, subject := read(tokenB.AccessToken); status != 200 || subject != bob.ID {
		t.Fatalf("verified Bob operation status=%d subject=%s", status, subject)
	}
	// Product policy changes now, without issuing a new token.
	forbidAlice.Store(true)
	if status, _ := read(tokenA.AccessToken); status == 200 {
		t.Fatal("stale product permission used through Bridge")
	}
	if status, subject := read(tokenB.AccessToken); status != 200 || subject != bob.ID {
		t.Fatal("Bob was affected by Alice permission change")
	}
	forbidAlice.Store(false)
	// A pending authorization created BEFORE logout must not survive loss of
	// the product-authenticated session, even with the original CSRF fields.
	pending, err := authorize(aliceSession.Token, "state-a")
	if err != nil {
		t.Fatal(err)
	}
	pendingHTML, err := io.ReadAll(io.LimitReader(pending.Body, 32<<10))
	pending.Body.Close()
	if err != nil || pending.StatusCode != http.StatusOK {
		t.Fatalf("pending consent status %d err=%v", pending.StatusCode, err)
	}
	txIDs, csrfValues := hiddenID.FindSubmatch(pendingHTML), hiddenCSRF.FindSubmatch(pendingHTML)
	if len(txIDs) != 2 || len(csrfValues) != 2 {
		t.Fatal("pending session transaction missing")
	}
	if err := idSvc.Logout(context.Background(), aliceSession.Token); err != nil {
		t.Fatal(err)
	}
	if status, subject := read(tokenA.AccessToken); status != 200 || subject != alice.ID {
		t.Fatal("logout revoked existing OAuth grant")
	}
	// Normal product logout must preserve an issued refresh token too:
	// refresh is a grant operation, not a continuation of the browser session.
	refreshValues := url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"real-go-oauth2"},
		"refresh_token": {tokenA.RefreshToken}, "resource": {resource},
	}
	refreshRequest, err := http.NewRequest(http.MethodPost, issuerHTTP.URL+"/token", strings.NewReader(refreshValues.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	refreshRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshResponse, err := clientBrowser.Do(refreshRequest)
	if err != nil {
		t.Fatal(err)
	}
	if refreshResponse.StatusCode != http.StatusOK {
		refreshResponse.Body.Close()
		t.Fatalf("normal logout revoked refresh grant: %d", refreshResponse.StatusCode)
	}
	var postLogoutTokens TokenResponse
	err = json.NewDecoder(io.LimitReader(refreshResponse.Body, 8192)).Decode(&postLogoutTokens)
	refreshResponse.Body.Close()
	if err != nil || !validToken(postLogoutTokens.AccessToken) {
		t.Fatalf("post-logout refresh invalid: %v", err)
	}
	if status, subject := read(postLogoutTokens.AccessToken); status != http.StatusOK || subject != alice.ID {
		t.Fatalf("post-logout refreshed credential rejected: %d %s", status, subject)
	}

	stale := url.Values{"transaction": {string(txIDs[1])}, "csrf": {string(csrfValues[1])}, "decision": {"allow"}}.Encode()
	request, err := http.NewRequest(http.MethodPost, issuerHTTP.URL+"/authorize", strings.NewReader(stale))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", issuerHTTP.URL)
	request.AddCookie(&http.Cookie{Name: identity.CookieName, Value: aliceSession.Token})
	staleResponse, err := clientBrowser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	staleResponse.Body.Close()
	if staleResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session approved pending consent after logout: %d", staleResponse.StatusCode)
	}

	begin, err = authorize(aliceSession.Token, "state-a")
	if err != nil {
		t.Fatal(err)
	}
	begin.Body.Close()
	if begin.StatusCode != http.StatusUnauthorized {
		t.Fatal("logged-out session resumed consent")
	}
	if err = issuer.Disconnect(context.Background(), alice.ID, grantIDByAccess(t, pool, tokenA.AccessToken)); err != nil {
		t.Fatal(err)
	}
	if status, _ := read(tokenA.AccessToken); status == 200 {
		t.Fatal("explicit disconnect failed to revoke access")
	}
	if status, _ := read(postLogoutTokens.AccessToken); status == http.StatusOK {
		t.Fatal("explicit disconnect did not revoke post-logout rotated access token")
	}

	if status, subject := read(tokenB.AccessToken); status != 200 || subject != bob.ID {
		t.Fatal("unrelated user grant revoked")
	}
	// This is a real Identity database mutation, not a mock permission flag.
	// Disabling a product account must take effect at the Bridge verifier
	// immediately, without waiting for OAuth access-token expiry.
	currentBob, err := idSvc.Account(context.Background(), "owner", bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := idSvc.SetEnabled(context.Background(), "owner", bob.ID, currentBob.Revision, false); err != nil {
		t.Fatal(err)
	}
	if status, _ := read(tokenB.AccessToken); status == http.StatusOK {
		t.Fatal("disabled real Identity account retained bearer access")
	}
}
func grantIDByAccess(t *testing.T, pool *pgxpool.Pool, access string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), "SELECT grant_id FROM oauth.access_tokens WHERE token_hash=$1", hash(access)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
