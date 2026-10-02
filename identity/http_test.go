// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

const webProofOrigin = "https://identity.example.test"

func webProofRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, webProofOrigin+path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Origin", webProofOrigin)
	r.Header.Set("X-Identity-Request", "1")
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.0.2.1:1234"
	return r
}

func webProofBoundary(t *testing.T) *Web {
	t.Helper()
	w, err := NewWeb(&Service{module: &Module{config: Config{Now: func() time.Time { return time.Unix(1700000000, 0) }}}}, webProofOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWebOriginConfiguration(t *testing.T) {
	for _, origin := range []string{"", "http://identity.example.test", "https://", webProofOrigin + "/", webProofOrigin + "?session=fixture", webProofOrigin + "#fragment", "https://user:fixture@identity.example.test", "https:identity.example.test"} {
		if _, err := NewWeb(&Service{}, origin); !errors.Is(err, ErrConfiguration) {
			t.Errorf("accepted invalid origin %q", origin)
		}
	}
	if _, err := NewWeb(nil, webProofOrigin); !errors.Is(err, ErrConfiguration) {
		t.Fatal("nil service accepted")
	}
	if _, err := NewWeb(&Service{}, webProofOrigin+":8443"); err != nil {
		t.Fatal("explicit HTTPS port rejected", err)
	}
}

func TestWebSameOriginBoundary(t *testing.T) {
	w := webProofBoundary(t)
	tests := []struct {
		name string
		edit func(*http.Request)
		want bool
	}{
		{"same origin", func(*http.Request) {}, true},
		{"same origin referer", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Set("Referer", webProofOrigin+"/admin?view=accounts")
		}, true},
		{"missing TLS", func(r *http.Request) { r.TLS = nil; r.Header.Set("X-Forwarded-Proto", "https") }, false},
		{"wrong host", func(r *http.Request) {
			r.Host = "attacker.example.test"
			r.Header.Set("X-Forwarded-Host", "identity.example.test")
		}, false},
		{"different origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example.test") }, false},
		{"origin suffix", func(r *http.Request) { r.Header.Set("Origin", webProofOrigin+".attacker.example.test") }, false},
		{"origin port", func(r *http.Request) { r.Header.Set("Origin", webProofOrigin+":443") }, false},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, false},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", webProofOrigin) }, false},
		{"duplicate empty origin", func(r *http.Request) { r.Header.Add("Origin", ""); r.Header.Set("Referer", webProofOrigin+"/admin") }, false},
		{"empty origin does not fall back", func(r *http.Request) { r.Header.Set("Origin", ""); r.Header.Set("Referer", webProofOrigin+"/admin") }, false},
		{"no source", func(r *http.Request) { r.Header.Del("Origin") }, false},
		{"HTTP referer", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Set("Referer", "http://identity.example.test/admin")
		}, false},
		{"foreign referer", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Set("Referer", "https://attacker.example.test/admin")
		}, false},
		{"userinfo referer", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Set("Referer", "https://user:fixture@identity.example.test/admin")
		}, false},
		{"duplicate referer", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Add("Referer", webProofOrigin+"/admin")
			r.Header.Add("Referer", webProofOrigin+"/other")
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := webProofRequest(http.MethodPost, "/login", "{}")
			tt.edit(r)
			if got := w.secureRequest(r); got != tt.want {
				t.Fatalf("same-origin result %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWebRejectsBeforeServiceWork(t *testing.T) {
	w := webProofBoundary(t)
	for _, edit := range []func(*http.Request){
		func(r *http.Request) { r.TLS = nil },
		func(r *http.Request) { r.Host = "attacker.example.test" },
		func(r *http.Request) { r.URL.RawQuery = "session=fixture-token" },
		func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example.test") },
		func(r *http.Request) { r.Header.Del("X-Identity-Request") },
		func(r *http.Request) { r.Header.Add("X-Identity-Request", "1") },
	} {
		r := webProofRequest(http.MethodPost, "/login", `{"login":"valid-handle","password":"a long test password"}`)
		edit(r)
		writer := httptest.NewRecorder()
		w.Handler().ServeHTTP(writer, r)
		if writer.Code != http.StatusUnauthorized || len(w.slots) != 0 || w.limits.global != 0 {
			t.Fatal("rejected request reached admission or authentication work")
		}
		for key, want := range map[string]string{"Cache-Control": "no-store", "Content-Type": "application/json", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
			if writer.Header().Get(key) != want {
				t.Errorf("missing response policy %s", key)
			}
		}
		if !strings.Contains(writer.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || len(writer.Result().Cookies()) != 0 {
			t.Fatal("unsafe framing policy or cookie on rejection")
		}
	}
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/csrf", http.StatusMethodNotAllowed},
		{http.MethodGet, "/login", http.StatusMethodNotAllowed},
		{http.MethodGet, "/rotate", http.StatusMethodNotAllowed},
		{http.MethodPut, "/logout", http.StatusMethodNotAllowed},
		{http.MethodHead, "/password", http.StatusMethodNotAllowed},
		{http.MethodPost, "/unknown", http.StatusNotFound},
	} {
		writer := httptest.NewRecorder()
		w.Handler().ServeHTTP(writer, webProofRequest(tt.method, tt.path, "{}"))
		if writer.Code != tt.status {
			t.Errorf("%s %s: got %d", tt.method, tt.path, writer.Code)
		}
	}
}

func TestWebAuthenticationIngressRejectsURLAndAmbiguity(t *testing.T) {
	// A nil service deliberately proves rejection precedes persisted lookup.
	w := &Web{host: "identity.example.test", origin: webProofOrigin}
	value := strings.Repeat("A", 43)
	for _, tt := range []struct {
		name     string
		mutation bool
		edit     func(*http.Request)
	}{
		{"HTTP", false, func(r *http.Request) { r.TLS = nil }},
		{"foreign host", false, func(r *http.Request) { r.Host = "attacker.example.test" }},
		{"read URL credential", false, func(r *http.Request) { r.URL.RawQuery = "session=" + value }},
		{"mutation URL credential", true, func(r *http.Request) { r.URL.RawQuery = "csrf=" + value }},
		{"missing cookie", false, func(r *http.Request) { r.Header.Del("Cookie") }},
		{"duplicate cookie", false, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: CookieName, Value: value}) }},
		{"malformed cookie", false, func(r *http.Request) { r.Header.Set("Cookie", CookieName+"=fixture") }},
		{"foreign mutation", true, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example.test") }},
		{"missing CSRF", true, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }},
		{"duplicate CSRF", true, func(r *http.Request) { r.Header.Add("X-CSRF-Token", value) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := webProofRequest(http.MethodPost, "/product-operation", "{}")
			r.AddCookie(&http.Cookie{Name: CookieName, Value: value})
			r.Header.Set("X-CSRF-Token", value)
			tt.edit(r)
			if p, err := w.AuthenticateRequest(r, tt.mutation); p != "" || !errors.Is(err, ErrAuthentication) {
				t.Fatal("unsafe request accepted", err)
			}
		})
	}
}

func TestWebJSONBounds(t *testing.T) {
	for _, tt := range []struct {
		name, contentType, body string
		duplicate, valid        bool
	}{
		{"valid", "application/json", `{"value":"fixture"}`, false, true},
		{"valid whitespace", "application/json", " {\"value\":\"fixture\"} \n", false, true},
		{"near body bound", "application/json", `{"value":"` + strings.Repeat("x", 2010) + `"}`, false, true},
		{"oversized", "application/json", `{"value":"` + strings.Repeat("x", 2048) + `"}`, false, false},
		{"empty", "application/json", "", false, false},
		{"unknown", "application/json", `{"value":"fixture","unknown":1}`, false, false},
		{"trailing object", "application/json", `{"value":"fixture"}{}`, false, false},
		{"trailing scalar", "application/json", `{"value":"fixture"} true`, false, false},
		{"malformed", "application/json", `{"value":`, false, false},
		{"wrong field type", "application/json", `{"value":1}`, false, false},
		{"missing content type", "", `{"value":"fixture"}`, false, false},
		{"form encoding", "application/x-www-form-urlencoded", "value=fixture", false, false},
		{"charset outside exact contract", "application/json; charset=utf-8", `{"value":"fixture"}`, false, false},
		{"duplicate content type", "application/json", `{"value":"fixture"}`, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := webProofRequest(http.MethodPost, "/login", tt.body)
			r.Header.Set("Content-Type", tt.contentType)
			if tt.duplicate {
				r.Header.Add("Content-Type", "application/json")
			}
			var target struct {
				Value string `json:"value"`
			}
			err := decodeWeb(httptest.NewRecorder(), r, &target)
			if (err == nil) != tt.valid || (err != nil && !errors.Is(err, ErrInvalid)) {
				t.Fatal("JSON acceptance/bounds", err)
			}
		})
	}
}

func TestWebCookieAndDeliberateSessionResponse(t *testing.T) {
	w := webProofBoundary(t)
	s, _ := newSession("opaque-account", 1, time.Unix(1700000000, 0).Add(time.Hour))
	writer := httptest.NewRecorder()
	w.setCookie(writer, s)
	cookies := writer.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	c := cookies[0]
	if c.Name != CookieName || c.Value != s.Token || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatal("cookie did not preserve host-only secure nonpersistent contract")
	}
	webSession(writer, s)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(writer.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 3 || body["principal"] == nil || body["csrf"] == nil || body["expires_at"] == nil || strings.Contains(writer.Body.String(), s.Token) {
		t.Fatal("unexpected session response fields or bearer disclosure")
	}
	var csrf string
	if err := json.Unmarshal(body["csrf"], &csrf); err != nil || csrf != s.CSRF {
		t.Fatal("missing deliberate synchronizer token")
	}
	cleared := httptest.NewRecorder()
	w.clearCookie(cleared)
	c = cleared.Result().Cookies()[0]
	if c.Value != "" || c.MaxAge != -1 || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("cleared cookie weakens scope or fails expiration")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("fixture", "session", s)
	for _, output := range []string{string(encoded), log.String(), fmt.Sprintf("%v %#v", s, s)} {
		if strings.Contains(output, s.Token) || strings.Contains(output, s.CSRF) {
			t.Fatal("general serialization disclosed session secret")
		}
	}
}

func TestWebErrorCategoriesStaySafe(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrAuthentication, 401, "authentication_failed"},
		{ErrInvalid, 400, "invalid_input"},
		{ErrLimited, 429, "authentication_limited"},
		{ErrConflict, 409, "authentication_conflict"},
		{ErrUnavailable, 503, "authentication_unavailable"},
		{context.Canceled, 503, "authentication_unavailable"},
		{context.DeadlineExceeded, 503, "authentication_unavailable"},
		{achrix.ErrAuthorizationUnavailable, 503, "authentication_unavailable"},
		{achrix.ErrNotReady, 503, "authentication_unavailable"},
		{achrix.ErrDenied, 403, "permission_denied"},
		{errors.New("fixture secret provider payload"), 401, "authentication_failed"},
	} {
		writer := httptest.NewRecorder()
		webError(writer, fmt.Errorf("fixture sensitive details: %w", tt.err))
		if writer.Code != tt.status || writer.Body.String() != "{\"code\":\""+tt.code+"\"}\n" {
			t.Fatalf("unsafe or incorrect error result: status %d", writer.Code)
		}
	}
}

func TestWebConcurrencyAdmissionDoesNotQueue(t *testing.T) {
	w := webProofBoundary(t)
	for range cap(w.slots) {
		w.slots <- struct{}{}
	}
	writer := httptest.NewRecorder()
	w.Handler().ServeHTTP(writer, webProofRequest(http.MethodPost, "/unknown", "{}"))
	if writer.Code != http.StatusTooManyRequests || len(w.slots) != cap(w.slots) || w.limits.global != 0 {
		t.Fatal("saturated ingress was queued or entered service work")
	}
	for range cap(w.slots) {
		<-w.slots
	}
	writer = httptest.NewRecorder()
	w.Handler().ServeHTTP(writer, webProofRequest(http.MethodPost, "/unknown", "{}"))
	if writer.Code != http.StatusNotFound || len(w.slots) != 0 {
		t.Fatal("slot release failed")
	}
}

func webProofDistinctPeers() []string {
	var seen [256]bool
	peers := make([]string, 0, 80)
	for n := 1; len(peers) < 80; n++ {
		ip := net.IPv4(192, 0, byte(n/256), byte(n%256))
		index := sha256.Sum256(ip.To16())[0]
		if !seen[index] {
			seen[index] = true
			peers = append(peers, net.JoinHostPort(ip.String(), "1234"))
		}
	}
	return peers
}

func TestLoginBudgetPeerGlobalAndClockBounds(t *testing.T) {
	now := time.Unix(1700000000, 0)
	var limits loginLimits
	for _, peer := range []string{"", "192.0.2.1", "attacker.example.test:1234", "not an address", "[invalid]:1234"} {
		if limits.allow(peer, now) {
			t.Fatal("malformed socket peer accepted")
		}
	}
	if limits.global != 0 {
		t.Fatal("malformed input consumed login budget")
	}
	for n := range 10 {
		if !limits.allow(fmt.Sprintf("192.0.2.1:%d", 1000+n), now) {
			t.Fatal("legitimate peer budget rejected early")
		}
	}
	if limits.allow("192.0.2.1:9999", now) || limits.allow("[::ffff:192.0.2.1]:1234", now) {
		t.Fatal("port or IPv4-mapped address evaded peer budget")
	}
	if limits.allow("192.0.2.2:1234", now.Add(-time.Nanosecond)) {
		t.Fatal("clock reversal reopened admission")
	}
	if limits.allow("192.0.2.1:1234", now.Add(time.Minute-time.Nanosecond)) {
		t.Fatal("window reset early")
	}
	if !limits.allow("192.0.2.1:1234", now.Add(time.Minute)) {
		t.Fatal("window did not reset at its bound")
	}
	limits = loginLimits{}
	peers := webProofDistinctPeers()
	for _, peer := range peers[:60] {
		if !limits.allow(peer, now) {
			t.Fatal("global budget rejected early")
		}
	}
	for range 10000 {
		if limits.allow(peers[60], now) {
			t.Fatal("global budget exceeded or overflowed")
		}
	}
	if limits.global != 60 {
		t.Fatal("denial changed global accounting")
	}
	if !limits.allow(peers[60], now.Add(time.Minute)) || limits.global != 1 {
		t.Fatal("global window failed to reset")
	}
}

func TestLoginBudgetConcurrentCardinalityAndSaturation(t *testing.T) {
	var limits loginLimits
	now := time.Unix(1700000000, 0)
	peers := webProofDistinctPeers()
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for n := range 2000 {
		workers.Go(func() {
			if limits.allow(peers[n%len(peers)], now) {
				accepted.Add(1)
			}
		})
	}
	workers.Wait()
	if accepted.Load() != 60 || limits.global != 60 {
		t.Fatal("concurrent global admission exceeded or lost capacity")
	}
	sum := 0
	for _, count := range limits.peers {
		if count > 10 {
			t.Fatal("peer bucket exceeded bound")
		}
		sum += int(count)
	}
	if sum != 60 || len(limits.peers) != 256 {
		t.Fatal("unbounded or inconsistent peer accounting")
	}
	limits.global = ^uint16(0)
	if limits.allow(peers[0], now) || limits.global != ^uint16(0) {
		t.Fatal("saturated global counter wrapped")
	}
	limits.global = 0
	for i := range limits.peers {
		limits.peers[i] = ^uint8(0)
	}
	if limits.allow(peers[0], now) || limits.global != 0 {
		t.Fatal("saturated peer counter wrapped")
	}
}

type webProofAdmissionModule struct {
	stopping chan struct{}
	release  chan struct{}
}

func (m *webProofAdmissionModule) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "fixture.authentication", Version: "0.1.0", Provides: []achrix.Capability{{ID: Authentication, Version: 1}}}
}
func (*webProofAdmissionModule) Start(context.Context) error { return nil }
func (*webProofAdmissionModule) Ready(context.Context) error { return nil }
func (m *webProofAdmissionModule) Stop(ctx context.Context) error {
	close(m.stopping)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWebAndDirectAuthenticationFollowCoreAdmission(t *testing.T) {
	m := &webProofAdmissionModule{stopping: make(chan struct{}), release: make(chan struct{})}
	var policyCalls atomic.Int32
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 5 * time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, capability, scope string) error {
		policyCalls.Add(1)
		if p != PublicPrincipal || capability != Authentication || scope != "" {
			return achrix.ErrDenied
		}
		return nil
	}), m)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(m.release) })
	service := &Service{app: app, module: &Module{config: Config{Now: func() time.Time { return time.Unix(1700000000, 0) }}}}
	w, err := NewWeb(service, webProofOrigin)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("A", 43)
	checkClosed := func() {
		t.Helper()
		calls := []func() error{
			func() error {
				_, err := service.Login(context.Background(), "fixture", "a long test password", "")
				return err
			},
			func() error { _, err := service.Authenticate(context.Background(), value); return err },
			func() error { _, err := service.ValidateCSRF(context.Background(), value, value); return err },
			func() error { _, err := service.RefreshCSRF(context.Background(), value); return err },
			func() error { _, err := service.Rotate(context.Background(), value); return err },
			func() error { return service.Logout(context.Background(), value) },
			func() error {
				return service.ChangePassword(context.Background(), value, "a long test password", "another long password")
			},
		}
		before := policyCalls.Load()
		for _, call := range calls {
			if err := call(); !errors.Is(err, achrix.ErrNotReady) {
				t.Fatal("direct authentication bypassed Core lifecycle", err)
			}
		}
		for _, path := range []string{"/login", "/csrf", "/rotate", "/logout", "/password"} {
			method := http.MethodPost
			if path == "/csrf" {
				method = http.MethodGet
			}
			r := webProofRequest(method, path, `{"login":"fixture","password":"a long test password"}`)
			r.AddCookie(&http.Cookie{Name: CookieName, Value: value})
			r.Header.Set("X-CSRF-Token", value)
			writer := httptest.NewRecorder()
			w.Handler().ServeHTTP(writer, r)
			if writer.Code != http.StatusServiceUnavailable || writer.Body.String() != "{\"code\":\"authentication_unavailable\"}\n" {
				t.Fatal("browser authentication bypassed Core lifecycle")
			}
		}
		if policyCalls.Load() != before {
			t.Fatal("closed Core invoked policy")
		}
	}
	checkClosed()
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A ready Core admits the explicit public attempt capability, then the actual
	// unstarted Identity module rejects. No DB/hash collaborator is available.
	if _, err := service.Login(context.Background(), "fixture", "a long test password", ""); !errors.Is(err, ErrUnavailable) || policyCalls.Load() != 1 {
		t.Fatal("public admission was not an explicit distinct policy decision", err)
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- app.Shutdown(context.Background()) }()
	select {
	case <-m.stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("Core shutdown never reached owned component")
	}
	checkClosed()
	releaseOnce.Do(func() { close(m.release) })
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	checkClosed()
}
