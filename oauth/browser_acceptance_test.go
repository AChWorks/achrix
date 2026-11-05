// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

// A real Chromium browser exercises cross-site navigation into an actual
// Identity.Web login, a product-owned localized consent screen, and a real
// OAuth2-client callback + Bridge protected operation. This is test-only and
// does not provide a product login or production-ready deployment.
func TestOAuthBrowserFixture(t *testing.T) {
	if os.Getenv("ACHRIX_OAUTH_BROWSER") != "1" {
		t.Skip("run through the explicitly isolated HTTPS Chromium browser runner")
	}
	root := os.Getenv("ACHRIX_OAUTH_BROWSER_ROOT")
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("private browser test root required")
	}
	if stat, err := os.Lstat(root); err != nil || !stat.IsDir() || stat.Mode().Perm() != 0700 || stat.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private mode-0700 non-symlink browser root required")
	}
	dsn := os.Getenv("ACHRIX_OAUTH_TEST_DSN")
	pgcfg := taskTestConfig(t, dsn, "achrix_oauth_test")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	checkTaskTestDatabase(t, ctx, pool, "achrix_oauth_test")
	for _, schema := range []string{"oauth", "identity", "audit"} {
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := audit.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := identity.Migrate(ctx, dsn); err != nil {
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
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, capability, target string) error {
		if p == identity.PublicPrincipal && capability == identity.Authentication && target == "" {
			return nil
		}
		if p == "browser-owner" && (capability == identity.AccountCreate || capability == identity.AccountRead || capability == audit.Append) {
			return nil
		}
		if p != "" && capability == identity.AccountRead && string(p) == target {
			return nil
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, am, im)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := app.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
	})
	auditSvc, err := audit.NewService(app, am)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewService(app, im, auditSvc)
	if err != nil {
		t.Fatal(err)
	}
	const login, password = "oauth-browser-user", "Synthetic oauth browser password 12345!"
	account, err := ids.CreateAccount(ctx, "browser-owner", login, password)
	if err != nil {
		t.Fatal(err)
	}

	// Issuer and client deliberately have different schemeful sites:
	// 127.0.0.1 vs localhost. This catches browser SameSite differences that
	// cannot be proven with an HTTP client's CookieJar.
	var issuerMux http.Handler
	issuerHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { issuerMux.ServeHTTP(w, r) }))
	t.Cleanup(issuerHTTP.Close)
	var clientMux http.Handler
	clientHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { clientMux.ServeHTTP(w, r) }))
	t.Cleanup(clientHTTP.Close)
	clientOrigin := "https://localhost" + strings.TrimPrefix(clientHTTP.URL, "https://127.0.0.1")
	callback := clientOrigin + "/callback"
	var protected http.Handler
	resourceHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { protected.ServeHTTP(w, r) }))
	t.Cleanup(resourceHTTP.Close)
	resource := resourceHTTP.URL + "/api"

	identityWeb, err := identity.NewWeb(ids, issuerHTTP.URL)
	if err != nil {
		t.Fatal(err)
	}
	session := func(ctx context.Context, r *http.Request) (Session, error) {
		cookie, err := r.Cookie(identity.CookieName)
		if err != nil || cookie.Value == "" {
			return Session{}, identity.ErrAuthentication
		}
		// Authenticate the actual bearer session on every OAuth page request.
		principal, err := ids.Authenticate(ctx, cookie.Value)
		if err != nil {
			return Session{}, err
		}
		return Session{Subject: string(principal), Binding: sha256.Sum256([]byte(cookie.Value))}, nil
	}
	permit := func(ctx context.Context, g Grant) error {
		current, err := ids.Account(ctx, "browser-owner", g.Subject)
		if err != nil || !current.Enabled {
			return ErrInvalidGrant
		}
		return app.Authorize(ctx, achrix.Principal(g.Subject), identity.AccountRead, g.Subject)
	}
	registered := Client{ID: "oauth-browser-client", Name: "Browser & Notes Client", RedirectURIs: []string{callback}, Scopes: []string{"notes.read"}, Enabled: true}
	issuer, err := New(Config{Issuer: issuerHTTP.URL, Resource: resource, Pool: pool, Clients: []Client{registered},
		ClientLookup: func(_ context.Context, id string) (Client, error) {
			if id != registered.ID {
				return Client{}, ErrInvalidGrant
			}
			return registered, nil
		},
		Session: session, Permit: permit,
		RenderConsent: func(w http.ResponseWriter, r *http.Request, v ConsentView) {
			fa := strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "fa")
			lang, direction, heading, approve, deny := "en", "ltr", "Authorize access", "Approve", "Deny"
			if fa {
				lang, direction, heading, approve, deny = "fa", "rtl", "تأیید دسترسی", "تأیید", "رد"
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!doctype html><html lang='%s' dir='%s'><meta charset='utf-8'><title>%s</title><main><h1>%s</h1><p>%s requests %s access to %s.</p><form method='post' action='%s'><input type='hidden' name='transaction' value='%s'><input type='hidden' name='csrf' value='%s'><button type='submit' name='decision' value='allow'>%s</button><button type='submit' name='decision' value='deny'>%s</button></form></main></html>",
				lang, direction, heading, heading, html.EscapeString(v.ClientName), html.EscapeString(strings.Join(v.Scopes, " ")), html.EscapeString(v.Resource), html.EscapeString(v.FormAction), html.EscapeString(v.Transaction), html.EscapeString(v.CSRF), approve, deny)
		},
		Diagnostic: func(context.Context, string, string, error) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A product's login continuation is independent of the Foundation issuer.
	// The Identity.Web cookie is deliberately Strict, so a cross-site initial
	// redirect may require a fresh interactive login before OAuth consent.
	mux := http.NewServeMux()
	mux.Handle("/auth/", http.StripPrefix("/auth", identityWeb.Handler()))
	mux.HandleFunc("GET /login.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, `"use strict";document.querySelector("form").addEventListener("submit",async e=>{e.preventDefault();const f=new FormData(e.currentTarget);const res=await fetch("/auth/login",{method:"POST",headers:{"Content-Type":"application/json","X-Identity-Request":"1"},body:JSON.stringify({login:f.get("login"),password:f.get("password")}),credentials:"same-origin"});if(!res.ok){document.getElementById("message").textContent="Login rejected";return;}location.assign(new URL(location.href).searchParams.get("return"));});`)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		value := r.URL.Query().Get("return")
		path, err := url.Parse(value)
		if err != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" || path.Path != issuer.AuthorizePath() || path.RawQuery == "" {
			http.Error(w, "invalid login continuation", http.StatusBadRequest)
			return
		}
		fa := strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "fa")
		lang, direction, title, goButton := "en", "ltr", "Sign in", "Sign in"
		if fa {
			lang, direction, title, goButton = "fa", "rtl", "ورود", "ورود"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'none'; form-action 'self'")
		fmt.Fprintf(w, "<!doctype html><html lang='%s' dir='%s'><meta charset='utf-8'><title>%s</title><main><h1>%s</h1><p id='message' role='status'></p><form><label for='login'>Username</label><input id='login' name='login' autocomplete='username' required><label for='password'>Password</label><input id='password' name='password' type='password' autocomplete='current-password' required><button type='submit'>%s</button></form></main><script src='/login.js'></script></html>", lang, direction, title, title, goButton)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == issuer.AuthorizePath() {
			if _, err := session(r.Context(), r); err != nil {
				http.Redirect(w, r, "/login?return="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
		}
		issuer.ServeHTTP(w, r)
	})
	issuerMux = mux

	route, err := bridge.JSONHandler[struct{}, identity.Account]("identity.account.read", func(r *http.Request) (struct{}, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/self" {
			return struct{}{}, errors.New("wrong method or path")
		}
		return struct{}{}, nil
	}, func(ctx context.Context, _ struct{}) (identity.Account, error) {
		verified, ok := bridge.RequestFromContext(ctx)
		if !ok {
			return identity.Account{}, errors.New("missing verified Bridge request")
		}
		return ids.Account(ctx, verified.Caller.Principal, string(verified.Caller.Principal))
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := http.NewServeMux()
	ops.Handle("GET /api/self", route)
	endpoint, err := bridge.NewJSONHTTP(bridge.Config{ResourceURL: resource, AuthorizationServers: []string{issuerHTTP.URL}, Scopes: []string{"notes.read"},
		Verify: func(ctx context.Context, token string) (bridge.Credential, error) {
			grant, expires, err := issuer.VerifyAccess(ctx, resource, token)
			if errors.Is(err, ErrInvalidGrant) {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			if err != nil {
				return bridge.Credential{}, err
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: achrix.Principal(grant.Subject), ClientID: grant.ClientID}, ExpiresAt: expires, Scopes: grant.Scopes}, nil
		},
		Admit:      func(context.Context, bridge.Caller) error { return nil },
		Diagnostic: func(context.Context, bridge.Diagnostic) {},
	}, ops)
	if err != nil {
		t.Fatal(err)
	}
	resourceMux := http.NewServeMux()
	resourceMux.Handle("GET /.well-known/oauth-protected-resource/api", endpoint.MetadataHandler())
	resourceMux.Handle("/", endpoint)
	protected = resourceMux

	clientOAuth := oauth2.Config{ClientID: registered.ID, RedirectURL: callback, Scopes: registered.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: issuerHTTP.URL + issuer.AuthorizePath(), TokenURL: issuerHTTP.URL + issuer.TokenPath(), AuthStyle: oauth2.AuthStyleInParams}}
	browserLoginState := "test-only-browser-client-state"
	clientRoutes := http.NewServeMux()
	clientRoutes.HandleFunc("GET /start", func(w http.ResponseWriter, r *http.Request) {
		auth := clientOAuth.AuthCodeURL(browserLoginState, oauth2.S256ChallengeOption(testVerifier), oauth2.SetAuthURLParam("resource", resource))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "<!doctype html><html><meta charset='utf-8'><main><h1>Client authorization</h1><a id='connect' href='%s'>Connect</a></main></html>", html.EscapeString(auth))
	})
	clientRoutes.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != browserLoginState || r.URL.Query().Get("iss") != issuerHTTP.URL || r.URL.Query().Get("error") != "" {
			http.Error(w, "invalid issuer/state", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if !validToken(code) {
			http.Error(w, "invalid authorization code", http.StatusBadRequest)
			return
		}
		clientContext := context.WithValue(r.Context(), oauth2.HTTPClient, issuerHTTP.Client())
		tokens, err := clientOAuth.Exchange(clientContext, code, oauth2.VerifierOption(testVerifier), oauth2.SetAuthURLParam("resource", resource))
		if err != nil {
			http.Error(w, "token exchange failed", http.StatusBadGateway)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, resource+"/self", nil)
		if err != nil {
			http.Error(w, "resource request failed", http.StatusInternalServerError)
			return
		}
		request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		response, err := resourceHTTP.Client().Do(request)
		if err != nil {
			http.Error(w, "resource access failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		var current identity.Account
		if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&current) != nil || current.ID != account.ID {
			http.Error(w, "protected product operation denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><meta charset='utf-8'><main><h1 id='outcome'>Bridge authorized</h1><p id='account'>%s</p></main></html>", html.EscapeString(current.Login))
	})
	clientMux = clientRoutes

	spkis := make([]string, 0, 3)
	for _, server := range []*httptest.Server{issuerHTTP, clientHTTP, resourceHTTP} {
		spki := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
		spkis = append(spkis, base64.StdEncoding.EncodeToString(spki[:]))
	}
	// Browser trust is scoped to pinned ephemeral SPKI hashes.
	metadata, err := json.Marshal(struct {
		Issuer   string   `json:"issuer"`
		Client   string   `json:"client"`
		SPKIs    []string `json:"spkis"`
		Login    string   `json:"login"`
		Password string   `json:"password"`
	}{issuerHTTP.URL, clientOrigin, spkis, login, password})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "oauth-fixture.json")
	if err := os.WriteFile(file+".pending", metadata, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file+".pending", file); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(180 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("OAuth browser did not finish before fixture deadline")
		case <-ticker.C:
			result, err := os.ReadFile(filepath.Join(root, "oauth-done"))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				t.Fatal(err)
			}
			if strings.TrimSpace(string(result)) != "PASS" {
				t.Fatal("OAuth Chromium/browser assertion failed")
			}
			return
		}
	}
}
