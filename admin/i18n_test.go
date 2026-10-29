// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func externalMessages(t *testing.T, domain, title string) fstest.MapFS {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema": 1, "locale": "ar", "name": "العربية", "direction": "rtl", "domain": domain,
		"messages": map[string]any{"administration": map[string]string{"other": title}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"ar.json": &fstest.MapFile{Data: data}}
}

func TestExternalLocaleSelectionFallbackAndEscapedBrowserData(t *testing.T) {
	payload := `</div><script>window.untrusted=1</script>`
	auth := &authentication{principal: "reader"}
	var seen string
	surface := testSurface(func(w http.ResponseWriter, _ *http.Request, view Request) {
		seen = view.Language
		if view.Localizer.Locale().Direction != "rtl" {
			t.Error("request lost locale metadata")
		}
		page := template.Must(template.New("owned").Parse(`{{define "content"}}<p>{{.}}</p>{{end}}`))
		if err := view.Render(Page{Title: "Mixed نمونه", Template: page, Data: "unchanged user content"}); err != nil {
			t.Error(err)
		}
	})
	s, err := New(Config{Origin: "https://admin.test", AuthPath: "/auth", Catalogs: []fs.FS{externalMessages(t, "admin", payload)}, SelectLanguage: func(r *http.Request) string { return r.Header.Get("X-UI-Language") }}, auth, &authorization{}, surface)
	if err != nil {
		t.Fatal(err)
	}
	available := s.Locales()
	if len(available) != 3 {
		t.Fatal("installed language discovery failed", available)
	}
	available[0].Tag = "modified"
	if s.Locales()[0].Tag == "modified" {
		t.Fatal("locale inventory exposes retained state")
	}
	r := tlsRequest("GET", "/admin/accounts")
	r.Header.Set("X-UI-Language", "ar")
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || seen != "ar" || !strings.Contains(body, `lang="ar" dir="rtl"`) || strings.Contains(body, payload) || !strings.Contains(body, "Accounts") || !strings.Contains(body, "unchanged user content") {
		t.Fatal("external locale did not preserve fallback, direction and escaping", w.Code, body)
	}
	start := strings.Index(body, `<div id="admin-messages" hidden>`)
	if start < 0 {
		t.Fatal("missing data bootstrap")
	}
	content := body[start+len(`<div id="admin-messages" hidden>`):]
	end := strings.Index(content, "</div>")
	if end < 0 {
		t.Fatal("invalid data bootstrap")
	}
	var messages struct {
		Locale  string `json:"locale"`
		Domains map[string]map[string]struct {
			Locale string `json:"locale"`
		} `json:"domains"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(content[:end])), &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Locale != "ar" || messages.Domains["test"]["accounts"].Locale != "en" || messages.Domains["admin"]["administration"].Locale != "ar" {
		t.Fatal("browser fallback lost message source locale", messages)
	}
	before := len(auth.mutations)
	r = tlsRequest("POST", "/admin/accounts/change")
	r.Header.Set("Origin", "https://admin.test")
	r.Header.Set("X-UI-Language", "uninstalled")
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 || len(auth.mutations) != before || !strings.Contains(w.Body.String(), `"code":"locale_unavailable"`) {
		t.Fatal("unsupported selection reached authentication/domain dispatch", w.Code)
	}
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `lang="en" dir="ltr"`) {
		t.Fatal("empty product selection did not use English default", w.Code)
	}
}

type observedFS struct {
	source fs.FS
	opens  int
}

func (s *observedFS) Open(name string) (fs.File, error) { s.opens++; return s.source.Open(name) }

func TestCatalogSnapshotPerformsNoRequestFilesystemIO(t *testing.T) {
	source := &observedFS{source: externalMessages(t, "admin", "Initial administration")}
	config := Config{Origin: "https://admin.test", AuthPath: "/auth", Language: "ar", Catalogs: []fs.FS{source}}
	surface := testSurface(func(http.ResponseWriter, *http.Request, Request) {})
	s, err := New(config, &authentication{principal: "reader"}, &authorization{}, surface)
	if err != nil {
		t.Fatal(err)
	}
	loaded := source.opens
	if loaded == 0 {
		t.Fatal("external source was not loaded")
	}
	source.source = externalMessages(t, "admin", "Reloaded administration")
	for i := 0; i < 3; i++ {
		w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
		s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/"))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Initial administration") || strings.Contains(w.Body.String(), "Reloaded administration") {
			t.Fatal("loaded snapshot changed implicitly", w.Code)
		}
	}
	if source.opens != loaded {
		t.Fatal("request performed filesystem I/O", loaded, source.opens)
	}
	reloaded, err := New(config, &authentication{principal: "reader"}, &authorization{}, surface)
	if err != nil {
		t.Fatal(err)
	}
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	reloaded.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Reloaded administration") {
		t.Fatal("explicit replacement did not reload data", w.Code)
	}
}

func TestUnrelatedProductMessagesStayOutsideBrowserPayload(t *testing.T) {
	messages := make(map[string]map[string]string)
	for i := 0; i < 40; i++ {
		messages[fmt.Sprintf("entry%d", i)] = map[string]string{"other": strings.Repeat("x", 4096)}
	}
	data, err := json.Marshal(map[string]any{"schema": 1, "locale": "en", "name": "English", "direction": "ltr", "domain": "unrelated", "messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Origin: "https://admin.test", AuthPath: "/auth", Catalogs: []fs.FS{fstest.MapFS{"en.json": &fstest.MapFile{Data: data}}}}, &authentication{principal: "reader"}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	if err != nil {
		t.Fatal(err)
	}
	for _, variants := range s.browser {
		for _, payload := range variants {
			if strings.Contains(payload, "unrelated") || strings.Contains(payload, "entry39") {
				t.Fatal("unrelated product domain leaked into an admin payload")
			}
		}
	}
}
