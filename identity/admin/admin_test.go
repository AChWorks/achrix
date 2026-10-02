// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/identity"
)

type accountStub struct {
	account  identity.Account
	err      error
	calls    []string
	actor    achrix.Principal
	target   string
	revision int64
}

func (s *accountStub) record(operation string, p achrix.Principal, target string) {
	s.calls = append(s.calls, operation)
	s.actor = p
	s.target = target
}
func (s *accountStub) Account(_ context.Context, p achrix.Principal, id string) (identity.Account, error) {
	s.record("id", p, id)
	return s.account, s.err
}
func (s *accountStub) LookupAccount(_ context.Context, p achrix.Principal, login string) (identity.Account, error) {
	s.record("login", p, login)
	return s.account, s.err
}
func (s *accountStub) CreateAccount(_ context.Context, p achrix.Principal, login, password string) (identity.Account, error) {
	s.record("create", p, login)
	return s.account, s.err
}
func (s *accountStub) SetEnabled(_ context.Context, p achrix.Principal, id string, revision int64, _ bool) error {
	s.record("enabled", p, id)
	s.revision = revision
	return s.err
}
func (s *accountStub) SetPassword(_ context.Context, p achrix.Principal, id string, revision int64, _ string) error {
	s.record("password", p, id)
	s.revision = revision
	return s.err
}
func (s *accountStub) RevokeAll(_ context.Context, p achrix.Principal, id string) error {
	s.record("revoke", p, id)
	return s.err
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	read time.Time
}

func (w *deadlineWriter) SetReadDeadline(d time.Time) error { w.read = d; return nil }
func view() shell.Request {
	return shell.Request{Principal: "operator", Language: "fa", Allowed: func(cap, target string) bool {
		return cap == identity.AccountSetEnabled && target == "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	}}
}
func post(t *testing.T, s *accountStub, path, body string) *deadlineWriter {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	serve(s, w, r, view())
	return w
}
func TestExactLoginAndIDLookupUseOwningServiceMetadata(t *testing.T) {
	s := &accountStub{account: identity.Account{ID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Login: "known.login", Enabled: true, Revision: 7}}
	for _, tt := range []struct{ path, body, operation, target string }{{"/lookup-login", `{"login":"known.login"}`, "login", "known.login"}, {"/lookup", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`, "id", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"}} {
		w := post(t, s, tt.path, tt.body)
		if w.Code != 200 || s.calls[len(s.calls)-1] != tt.operation || s.actor != "operator" || s.target != tt.target {
			t.Fatal("presentation bypassed owning contract", w.Code, s.calls)
		}
		var got result
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Account.Login != s.account.Login || got.Account.Revision != 7 || !got.Permissions["enabled"] || got.Permissions["password"] || got.Permissions["revoke"] {
			t.Fatal("safe metadata/permission hints lost", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "session") {
			t.Fatal("credential/session material exposed")
		}
	}
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{{identity.ErrNotFound, 404, "not_found"}, {achrix.ErrDenied, 403, "permission_denied"}, {errors.New("private-provider-error"), 503, "unavailable"}} {
		s.err = tt.err
		w := post(t, s, "/lookup-login", `{"login":"known.login"}`)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) || strings.Contains(w.Body.String(), "private-provider-error") {
			t.Fatal("unsafe lookup failure semantics", w.Code, w.Body.String())
		}
	}
}
func TestStrictJSONPrecedesDomainOperations(t *testing.T) {
	for _, body := range []string{`{"login":"known","extra":"ignored"}`, `{"login":"known"} {}`, `{`, strings.Repeat("x", 2049)} {
		s := &accountStub{}
		w := post(t, s, "/lookup-login", body)
		if w.Code != 400 || len(s.calls) != 0 {
			t.Fatal("invalid JSON reached owning service", w.Code)
		}
	}
	s := &accountStub{}
	r := httptest.NewRequest("POST", "/lookup-login", strings.NewReader(`{"login":"known"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	serve(s, w, r, view())
	if w.Code != 400 || len(s.calls) != 0 {
		t.Fatal("unreviewed content type accepted")
	}
	r = httptest.NewRequest("POST", "/lookup-login", strings.NewReader(`{"login":"known"}`))
	r.Header.Set("Content-Type", "application/json")
	plain := httptest.NewRecorder()
	serve(s, plain, r, view())
	if plain.Code != 503 || len(s.calls) != 0 {
		t.Fatal("unsupported actual transport deadline reached service")
	}
}
func TestKnownAccountMutationPreconditionsPreserved(t *testing.T) {
	s := &accountStub{}
	for _, tt := range []struct {
		path, body, operation string
		revision              int64
	}{{"/enabled", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"3","enabled":false}`, "enabled", 3}, {"/password", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"4","password":"new long password for test"}`, "password", 4}, {"/revoke", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`, "revoke", 4}} {
		w := post(t, s, tt.path, tt.body)
		if w.Code != 204 || s.calls[len(s.calls)-1] != tt.operation || s.actor != "operator" || s.target != "ABCDEFGHIJKLMNOPQRSTUVWXYZ" || s.revision != tt.revision {
			t.Fatal("mutation contract changed", w.Code, s.calls, s.revision)
		}
	}
	before := len(s.calls)
	w := post(t, s, "/enabled", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"3"}`)
	if w.Code != 400 || len(s.calls) != before {
		t.Fatal("absent enabled treated as false")
	}
	// RevokeAll has no revision precondition; a claimed revision is rejected.
	w = post(t, s, "/revoke", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"4"}`)
	if w.Code != 400 || len(s.calls) != before {
		t.Fatal("RevokeAll silently claims CAS")
	}
}
func TestRevisionAboveJavaScriptPrecisionRoundTripsExactly(t *testing.T) {
	const revision int64 = 9007199254740993
	s := &accountStub{account: identity.Account{ID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Login: "known.login", Revision: revision}}
	w := post(t, s, "/lookup", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revision":"9007199254740993"`) {
		t.Fatal("revision response lost exact decimal identity", w.Body.String())
	}
	w = post(t, s, "/enabled", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"9007199254740993","enabled":true}`)
	if w.Code != 204 || s.revision != revision {
		t.Fatal("revision request lost int64 precondition", w.Code, s.revision)
	}
	for _, body := range []string{`{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":9007199254740993,"enabled":true}`, `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"9223372036854775808","enabled":true}`, `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"9e15","enabled":true}`} {
		before := len(s.calls)
		w = post(t, s, "/enabled", body)
		if w.Code != 400 || len(s.calls) != before {
			t.Fatal("unsafe numeric/overflow revision reached domain", w.Code)
		}
	}
}

func TestPresentationRemainsModuleOwnedAndLocalized(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, shell.ErrConfiguration) {
		t.Fatal("nil service accepted")
	}
	if _, err := Authenticator(nil); !errors.Is(err, shell.ErrConfiguration) {
		t.Fatal("nil web accepted")
	}
	for _, language := range []string{"en", "fa"} {
		s := &accountStub{}
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		v := view()
		v.Language = language
		v.Render = func(page shell.Page) error {
			if page.Template != accountTemplate {
				t.Fatal("forms left module ownership")
			}
			return page.Template.ExecuteTemplate(w, "content", page.Data)
		}
		serve(s, w, r, v)
		html := w.Body.String()
		if !strings.Contains(html, `id="lookup-login"`) || !strings.Contains(html, `data-read-only`) || !strings.Contains(html, `dir="ltr"`) || strings.Contains(html, `id="new-password"`) {
			t.Fatal("lookup or denied-create presentation failed", language)
		}
	}
}
