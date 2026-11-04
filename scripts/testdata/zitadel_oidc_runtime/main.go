// SPDX-License-Identifier: MPL-2.0
// Non-production upstream SDK HTTP behavior probe; no product credentials or state.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/zitadel/oidc/v3/example/server/exampleop"
	"github.com/zitadel/oidc/v3/example/server/storage"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func main() {
	const issuer = "https://auth.example.test"
	storage.RegisterClients(storage.NativeClient("test", "https://client.example.test/callback"))
	handler := exampleop.SetupServer(issuer, storage.NewStorage(storage.NewUserStore(issuer)), nil, false)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, issuer+path, nil))
		fmt.Fprintf(os.Stderr, "DISCOVERY path=%s status=%d content_type=%q\n", path, response.Code, response.Header().Get("Content-Type"))
		if path == "/.well-known/openid-configuration" {
			if _, err := os.Stdout.Write(response.Body.Bytes()); err != nil {
				panic(err)
			}
		}
	}
	// The SDK demo config is intentionally not a product security profile.
	// Observe whether its HTTP authorization boundary enforces AChrix-required
	// S256 and resource presence before login, without completing a grant.
	for _, withS256 := range []bool{false, true} {
		args := url.Values{
			"response_type": {"code"}, "client_id": {"test"},
			"redirect_uri": {"https://client.example.test/callback"},
			"scope":        {"openid"}, "state": {"demo-state"},
		}
		if withS256 {
			args.Set("code_challenge", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
			args.Set("code_challenge_method", "S256")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, issuer+"/auth?"+args.Encode(), nil))
		location, err := url.Parse(response.Header().Get("Location"))
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "AUTHORIZE with_s256=%v with_resource=false status=%d redirect_path=%s\n", withS256, response.Code, location.Path)
	}

	// The SDK model accepts a resource parameter for token exchange grants,
	// but does not carry RFC 8707 resource on the authorization-code request
	// or its token redemption request. This is API shape, not a security proof.
	_, authHasResource := reflect.TypeOf(oidc.AuthRequest{}).FieldByName("Resource")
	_, codeHasResource := reflect.TypeOf(oidc.AccessTokenRequest{}).FieldByName("Resource")
	fmt.Fprintf(os.Stderr, "RESOURCE_MODEL authorize=%v code_token=%v\n", authHasResource, codeHasResource)

	if authHasResource || codeHasResource {
		panic("The upstream resource data model changed; re-evaluate RFC 8707 fit")
	}
	const resourceA = "https://resource-a.example.test"
	const resourceB = "https://resource-b.example.test"
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	for _, scenario := range []struct {
		name                                                 string
		pkce                                                 bool
		authResource, tokenResource, verifier, tokenRedirect string
		expectedToken                                        int
		replay                                               bool
	}{
		{"no_pkce_no_resource", false, "", "", "", "", 400, false},
		{"s256_same_resource", true, resourceA, resourceA, verifier, "", 200, true},
		{"s256_no_resource", true, "", "", verifier, "", 200, false},
		{"s256_wrong_resource", true, resourceA, resourceB, verifier, "", 200, false},
		{"s256_missing_token_resource", true, resourceA, "", verifier, "", 200, false},
		{"s256_missing_auth_resource", true, "", resourceA, verifier, "", 200, false},
		{"s256_wrong_verifier", true, resourceA, resourceA, "xBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "", 400, false},
		{"s256_wrong_redirect", true, resourceA, resourceA, verifier, "https://unregistered.example.test/callback", 400, false},
	} {
		status, replayStatus, hasIssuer := runCodeFlow(handler, issuer, scenario.name, scenario.pkce,
			scenario.authResource, scenario.tokenResource, scenario.verifier, scenario.tokenRedirect, scenario.replay)
		if status != scenario.expectedToken || hasIssuer {
			panic(fmt.Sprintf("Upstream behavior changed in %s: status=%d, issuer_parameter=%v", scenario.name, status, hasIssuer))
		}
		if scenario.replay && replayStatus != 400 {
			panic(fmt.Sprintf("Expected demo code replay rejection in %s, got %d", scenario.name, replayStatus))
		}
	}
}

func request(handler http.Handler, method, address string, form url.Values) *httptest.ResponseRecorder {
	body := strings.NewReader("")
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, address, body)
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func runCodeFlow(handler http.Handler, issuer, name string, withS256 bool, authResource, tokenResource, verifier, tokenRedirect string, replay bool) (int, int, bool) {
	const redirect = "https://client.example.test/callback"
	args := url.Values{
		"response_type": {"code"}, "client_id": {"test"}, "redirect_uri": {redirect},
		"scope": {"openid"}, "state": {"demo-state"},
	}
	if withS256 {
		args.Set("code_challenge", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
		args.Set("code_challenge_method", "S256")
	}
	if authResource != "" {
		args.Set("resource", authResource)
	}
	auth := request(handler, http.MethodGet, issuer+"/auth?"+args.Encode(), nil)
	loginURI, err := url.Parse(auth.Header().Get("Location"))
	if err != nil || auth.Code != 302 || loginURI.Path != "/login/username" {
		panic(fmt.Sprintf("authorization request unexpectedly rejected in %s (status=%d)", name, auth.Code))
	}
	id := loginURI.Query().Get("authRequestID")
	if id == "" {
		panic("missing pending authorization request ID")
	}
	loginForm := url.Values{"id": {id}, "username": {"test-user@auth.example.test"}, "password": {"verysecure"}}
	login := request(handler, http.MethodPost, issuer+"/login/username", loginForm)
	if login.Code != 302 {
		panic(fmt.Sprintf("demo login failed in %s (status=%d)", name, login.Code))
	}
	callback := request(handler, http.MethodGet, login.Header().Get("Location"), nil)
	if callback.Code != 302 {
		panic(fmt.Sprintf("authorization callback failed in %s (status=%d)", name, callback.Code))
	}
	redirectURI, err := url.Parse(callback.Header().Get("Location"))
	if err != nil || redirectURI.Scheme != "https" || redirectURI.Host != "client.example.test" {
		panic(fmt.Sprintf("unexpected authorization redirect in %s", name))
	}
	code := redirectURI.Query().Get("code")
	if code == "" || redirectURI.Query().Get("state") != "demo-state" {
		panic(fmt.Sprintf("missing code/state in %s", name))
	}
	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"test"}, "code": {code},
		"redirect_uri": {redirect},
	}
	if verifier != "" {
		tokenForm.Set("code_verifier", verifier)
	}
	if tokenResource != "" {
		tokenForm.Set("resource", tokenResource)
	}
	if tokenRedirect != "" {
		tokenForm.Set("redirect_uri", tokenRedirect)
	}
	token := request(handler, http.MethodPost, issuer+"/oauth/token", tokenForm)
	if token.Code == 200 {
		var data struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(token.Body.Bytes(), &data); err != nil || data.AccessToken == "" {
			panic(fmt.Sprintf("token success without parsed access token in %s", name))
		}
	}
	replayStatus := 0
	if replay {
		replayStatus = request(handler, http.MethodPost, issuer+"/oauth/token", tokenForm).Code
	}
	hasIssuer := redirectURI.Query().Get("iss") != ""
	fmt.Fprintf(os.Stderr, "CODE_FLOW case=%s auth=%d login=%d callback=%d token=%d replay=%d response_iss=%v\n",
		name, auth.Code, login.Code, callback.Code, token.Code, replayStatus, hasIssuer)
	return token.Code, replayStatus, hasIssuer
}
