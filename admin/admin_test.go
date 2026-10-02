// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"context"
	"crypto/tls"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

type authentication struct {
	principal achrix.Principal
	err       error
	mutations []bool
}

func (a *authentication) AuthenticateRequest(_ *http.Request, mutation bool) (achrix.Principal, error) {
	a.mutations = append(a.mutations, mutation)
	return a.principal, a.err
}

type authorization struct{ err error }

func (a *authorization) Authorize(context.Context, achrix.Principal, string, string) error {
	return a.err
}

type transportRecorder struct {
	*httptest.ResponseRecorder
	read, write time.Time
	deadlineErr error
}

func (w *transportRecorder) SetReadDeadline(d time.Time) error  { w.read = d; return w.deadlineErr }
func (w *transportRecorder) SetWriteDeadline(d time.Time) error { w.write = d; return w.deadlineErr }
func tlsRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, "https://admin.test"+path, nil)
	r.TLS = &tls.ConnectionState{}
	return r
}
func testSurface(handler func(http.ResponseWriter, *http.Request, Request)) Surface {
	return Surface{ID: "accounts", Title: Text{English: "Accounts", Persian: "حساب‌ها"}, Capability: "test.read", Handler: handler}
}
func fixture(t *testing.T, auth *authentication, policy *authorization, surface Surface) *Shell {
	t.Helper()
	s, e := New(Config{Origin: "https://admin.test", AuthPath: "/auth", Language: "fa"}, auth, policy, surface)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestConstructorRejectsTypedNilAndInvalidRegistration(t *testing.T) {
	var missing *authentication
	var missingPolicy *authorization
	surface := testSurface(func(http.ResponseWriter, *http.Request, Request) {})
	for _, tt := range []struct {
		a Authenticator
		p Authorizer
	}{{missing, &authorization{}}, {&authentication{}, missingPolicy}, {nil, &authorization{}}} {
		if _, e := New(Config{Origin: "https://admin.test", AuthPath: "/auth"}, tt.a, tt.p, surface); !errors.Is(e, ErrConfiguration) {
			t.Fatal("typed nil accepted")
		}
	}
	for _, config := range []Config{{Origin: "http://admin.test", AuthPath: "/auth"}, {Origin: "https://admin.test/", AuthPath: "/auth"}, {Origin: "https://admin.test", AuthPath: "/admin/auth"}, {Origin: "https://admin.test", AuthPath: "//auth"}, {Origin: "https://admin.test", AuthPath: "/auth", Language: "unknown"}} {
		if _, e := New(config, &authentication{}, &authorization{}, surface); !errors.Is(e, ErrConfiguration) {
			t.Fatal("invalid config accepted", config)
		}
	}
	if _, e := New(Config{Origin: "https://admin.test", AuthPath: "/auth"}, &authentication{}, &authorization{}, surface, surface); !errors.Is(e, ErrConfiguration) {
		t.Fatal("duplicate surface accepted")
	}
}
func TestOriginPresenceFailsClosedBeforeAuthentication(t *testing.T) {
	for _, origin := range [][]string{nil, {}, {""}, {"https://evil.test"}, {"https://admin.test", "https://admin.test"}} {
		auth := &authentication{principal: "reader"}
		s := fixture(t, auth, &authorization{}, testSurface(func(w http.ResponseWriter, _ *http.Request, _ Request) { w.WriteHeader(204) }))
		r := tlsRequest("POST", "/admin/accounts/change")
		r.Header["Origin"] = origin
		r.Header.Set("Referer", "https://admin.test/admin/accounts")
		w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 || len(auth.mutations) != 0 {
			t.Fatal("present invalid Origin fell back to Referer", origin, w.Code)
		}
	}
	auth := &authentication{principal: "reader"}
	s := fixture(t, auth, &authorization{}, testSurface(func(w http.ResponseWriter, _ *http.Request, _ Request) { w.WriteHeader(204) }))
	r := tlsRequest("POST", "/admin/accounts/change")
	r.Header.Set("Referer", "https://admin.test/admin/accounts")
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 204 || len(auth.mutations) != 1 || !auth.mutations[0] {
		t.Fatal("absent Origin valid Referer failed", w.Code)
	}
}
func TestAdmissionAndTransportBounds(t *testing.T) {
	called := false
	auth := &authentication{principal: "reader"}
	s := fixture(t, auth, &authorization{}, testSurface(func(w http.ResponseWriter, _ *http.Request, _ Request) { called = true; w.WriteHeader(204) }))
	r := tlsRequest("GET", "/admin/accounts")
	ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(200*time.Millisecond))
	defer cancel()
	r = r.WithContext(ctx)
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	d, _ := ctx.Deadline()
	if w.Code != 204 || !called || !w.read.Equal(d) || !w.write.Equal(d) {
		t.Fatal("transport exceeds earlier caller deadline", w.read, w.write, d)
	}
	called = false
	r = tlsRequest("POST", "/admin/accounts")
	r.Header.Set("Origin", "https://admin.test")
	r.ContentLength = (12 << 20) + 1
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 413 || called {
		t.Fatal("oversized body reached surface")
	}
	called = false
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder(), deadlineErr: http.ErrNotSupported}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 503 || called {
		t.Fatal("unsupported transport admitted surface")
	}
	for i := 0; i < cap(s.slots); i++ {
		s.slots <- struct{}{}
	}
	defer func() {
		for len(s.slots) > 0 {
			<-s.slots
		}
	}()
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 429 {
		t.Fatal("saturated shell queues work")
	}
}
func TestAuthorizationErrorsAndProtectedRouting(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{{achrix.ErrDenied, 403}, {errors.New("provider-secret"), 503}} {
		called := false
		s := fixture(t, &authentication{principal: "reader"}, &authorization{err: tt.err}, testSurface(func(http.ResponseWriter, *http.Request, Request) { called = true }))
		w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
		s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
		if w.Code != tt.status || called || strings.Contains(w.Body.String(), "provider-secret") {
			t.Fatal("permission/provider failure admitted operation", w.Code)
		}
	}
	for _, tt := range []struct {
		err    error
		status int
	}{{ErrUnauthenticated, 303}, {errors.New("session-backend-secret"), 503}} {
		s := fixture(t, &authentication{err: tt.err}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
		w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
		s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
		if w.Code != tt.status || strings.Contains(w.Body.String(), "session-backend-secret") {
			t.Fatal("authentication category changed", w.Code)
		}
	}
}
func TestRendererEscapesDataAndEmitsLocalizedSemanticHeaders(t *testing.T) {
	page := template.Must(template.New("owned").Parse(`{{define "content"}}<p>{{.}}</p>{{end}}`))
	payload := `<script>alert("secret")</script>`
	s := fixture(t, &authentication{principal: "reader"}, &authorization{}, testSurface(func(_ http.ResponseWriter, _ *http.Request, r Request) {
		if e := r.Render(Page{Title: "Mixed نمونه", Template: page, Data: payload}); e != nil {
			t.Error(e)
		}
	}))
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 200 || strings.Contains(w.Body.String(), payload) || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), `lang="fa" dir="rtl"`) || !strings.Contains(w.Body.String(), `aria-current="page"`) || !strings.Contains(w.Body.String(), `href="#main"`) {
		t.Fatal("escaping/semantic RTL contract failed", w.Body.String())
	}
	for header, value := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
		if w.Header().Get(header) != value {
			t.Fatal("missing browser protection", header)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("unsafe CSP")
	}
	large := strings.Repeat("x", 128<<10)
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	if e := s.render(w, Page{Title: "large", Template: page, Data: large}, nil, "accounts"); e == nil || w.Body.Len() != 0 {
		t.Fatal("oversized template streamed partial response")
	}
}
