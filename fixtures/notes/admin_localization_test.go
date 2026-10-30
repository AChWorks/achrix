// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/admin/localize"
	"github.com/AChWorks/achrix/identity"
	identityadmin "github.com/AChWorks/achrix/identity/admin"
	"github.com/AChWorks/achrix/media"
	mediaadmin "github.com/AChWorks/achrix/media/admin"
)

// This runs inside the already-built opt-in fixture executable. Its new locale
// files are created at runtime; real Module constructors and session APIs are
// consumed without copied runtime source or private dependency imports.
func adminRuntimeCatalogProof(t *testing.T, root string, app *achrix.Application, ids *identity.Service, ms *media.Service, administrator achrix.Principal) {
	t.Helper()
	directory := filepath.Join(root, "runtime-catalogs")
	if err := os.Mkdir(directory, 0700); err != nil {
		browserFixtureFatal(t, "create runtime catalog directory failed", err)
	}
	translations := map[string]map[string]string{
		"admin":    {"sign_in": "Third sign in", "administration": "Third administration", "working": "Third working"},
		"identity": {"accounts.title": "Third accounts", "state.enabled": "Third enabled"},
		"media":    {"library.title": "Third file library", "state.ready": "Third ready"},
	}
	for domain, messages := range translations {
		forms := map[string]localize.Forms{}
		for key, value := range messages {
			forms[key] = localize.Forms{Other: value}
		}
		data, err := json.Marshal(struct {
			Schema    int                       `json:"schema"`
			Locale    string                    `json:"locale"`
			Name      string                    `json:"name"`
			Direction string                    `json:"direction"`
			Domain    string                    `json:"domain"`
			Messages  map[string]localize.Forms `json:"messages"`
		}{1, "ar", "Arabic proof", "rtl", domain, forms})
		if err != nil {
			browserFixtureFatal(t, "encode runtime catalog failed", err)
		}
		if err = os.WriteFile(filepath.Join(directory, domain+".ar.json"), data, 0600); err != nil {
			browserFixtureFatal(t, "write runtime catalog failed", err)
		}
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "https://" + server.Listener.Addr().String()
	web, err := identity.NewWeb(ids, origin)
	if err != nil {
		server.Close()
		browserFixtureFatal(t, "construct localized Identity Web failed", err)
	}
	auth, err := identityadmin.Authenticator(web)
	if err != nil {
		server.Close()
		browserFixtureFatal(t, "construct localized authenticator failed", err)
	}
	identitySurface, err := identityadmin.New(ids)
	if err != nil {
		server.Close()
		browserFixtureFatal(t, "construct localized Identity surface failed", err)
	}
	mediaSurface, err := mediaadmin.New(ms)
	if err != nil {
		server.Close()
		browserFixtureFatal(t, "construct localized Media surface failed", err)
	}
	shell, err := admin.New(admin.Config{Origin: origin, AuthPath: "/auth", Language: "ar", Catalogs: []fs.FS{os.DirFS(directory)}}, auth, app, identitySurface, mediaSurface)
	if err != nil {
		server.Close()
		browserFixtureFatal(t, "construct localized Admin shell failed", err)
	}
	if len(shell.Locales()) != 3 {
		server.Close()
		browserFixtureFatal(t, "real surface catalog discovery lost new locale")
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/", http.StripPrefix("/auth", web.Handler()))
	mux.Handle("/admin", shell.Handler())
	mux.Handle("/admin/", shell.Handler())
	server.Config.Handler = mux
	admin.ConfigureServer(server.Config)
	server.StartTLS()
	defer server.Close()
	get := func(path string, cookie *http.Cookie) string {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			browserFixtureFatal(t, "construct localized HTTPS request failed", err)
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		client := server.Client()
		client.Timeout = 6 * time.Second
		response, err := client.Do(request)
		if err != nil {
			browserFixtureFatal(t, "execute localized HTTPS request failed", err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
		if err != nil || response.StatusCode != http.StatusOK || len(data) > 128<<10 {
			browserFixtureFatal(t, fmt.Sprintf("real localized surface request failed (HTTP %d)", response.StatusCode), err)
		}
		page := string(data)
		if !strings.Contains(page, `lang="ar" dir="rtl"`) {
			browserFixtureFatal(t, "real surface did not use installed locale direction")
		}
		if response.Header.Get("Content-Security-Policy") == "" || response.Header.Get("Cache-Control") != "no-store" {
			browserFixtureFatal(t, "localization changed response security policy")
		}
		return page
	}
	if !strings.Contains(get("/admin/login", nil), "Third sign in") {
		browserFixtureFatal(t, "real login did not render installed translation")
	}
	credentials, _ := json.Marshal(map[string]string{"login": adminBrowserLogin, "password": adminBrowserPassword})
	cookie, _ := fixtureSession(t, identityRequest(t, server, http.MethodPost, "/auth/login", origin, "", string(credentials), nil), administrator)
	for _, screen := range []struct{ path, domain, title, key, translated string }{
		{"/admin/identity", "identity", "Third accounts", "state.enabled", "Third enabled"},
		{"/admin/media", "media", "Third file library", "state.ready", "Third ready"},
	} {
		page := get(screen.path, cookie)
		if !strings.Contains(page, "<h1>"+screen.title+"</h1>") {
			browserFixtureFatal(t, "real module title was not translated: "+screen.domain)
		}
		_, after, ok := strings.Cut(page, `<div id="admin-messages" hidden>`)
		encoded, _, complete := strings.Cut(after, "</div>")
		if !ok || !complete {
			browserFixtureFatal(t, "safe browser catalog missing: "+screen.domain)
		}
		var catalog struct {
			Locale  string                                 `json:"locale"`
			Domains map[string]map[string]localize.Message `json:"domains"`
		}
		if err = json.Unmarshal([]byte(html.UnescapeString(encoded)), &catalog); err != nil {
			browserFixtureFatal(t, "invalid safe browser catalog", err)
		}
		message := catalog.Domains[screen.domain][screen.key]
		if catalog.Locale != "ar" || message.Locale != "ar" || message.Forms.Other != screen.translated || catalog.Domains["admin"]["working"].Forms.Other != "Third working" {
			browserFixtureFatal(t, "module/browser translation source differs: "+screen.domain)
		}
		if screen.domain == "media" && catalog.Domains["media"]["status.result"].Locale != "en" {
			browserFixtureFatal(t, "fallback browser message lost English source locale")
		}
	}
	t.Log("post-build installed third RTL catalogs render actual Identity/Media pages and browser message snapshots through real HTTPS session")
}
