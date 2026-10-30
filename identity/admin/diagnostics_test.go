// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/identity"
)

type bodyFault struct{ timeout bool }

func (e bodyFault) Error() string   { return "synthetic private body transport credential" }
func (e bodyFault) Timeout() bool   { return e.timeout }
func (e bodyFault) Temporary() bool { return false }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestDecoderDistinguishesTimeoutIOAndInvalidInputBeforeService(t *testing.T) {
	for _, tt := range []struct {
		name   string
		body   io.Reader
		code   string
		status int
	}{
		{"first timeout", errorReader{bodyFault{true}}, "request_timeout", 408},
		{"trailing timeout", io.MultiReader(strings.NewReader(`{"login":"known"}`), errorReader{bodyFault{true}}), "request_timeout", 408},
		{"body I/O", errorReader{bodyFault{}}, "unavailable", 503},
		{"caller canceled", errorReader{context.Canceled}, "request_canceled", 408},
		{"syntax", strings.NewReader(`{"login":`), "invalid_input", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &accountStub{}
			r := httptest.NewRequest("POST", "/lookup-login", tt.body)
			r.Header.Set("Content-Type", "application/json")
			w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			serve(s, w, r, view())
			if w.Code != tt.status || len(s.calls) != 0 || !strings.Contains(w.Body.String(), `"code":"`+tt.code+`"`) || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) {
				t.Fatal("incorrect pre-Service failure", w.Code, w.Body.String(), s.calls)
			}
			if strings.Contains(w.Body.String(), "private body transport") {
				t.Fatal("raw body error exposed")
			}
		})
	}
}

func TestInputMetadataRetainsSafeFieldAndReasonWithoutBodyValues(t *testing.T) {
	s := &accountStub{}
	w := post(t, s, "/enabled", `{"id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","revision":"3"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"enabled"`) || !strings.Contains(w.Body.String(), `"reason":"required"`) {
		t.Fatal("missing field not actionable", w.Body.String())
	}
	w = post(t, s, "/create", `{"login":42,"password":"private-body-password"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"login"`) || strings.Contains(w.Body.String(), "private-body-password") {
		t.Fatal("type validation lacks safe field or leaks body", w.Body.String())
	}
	w = post(t, s, "/create", `{"login":"known","private-unknown-field":"private-value"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"reason":"unknown_field"`) || strings.Contains(w.Body.String(), "private-") {
		t.Fatal("unknown JSON property leaked or lost category", w.Body.String())
	}
	w = post(t, s, "/create", `{"login":"known","password":"`+strings.Repeat("p", 2500)+`"}`)
	if w.Code != 413 || !strings.Contains(w.Body.String(), `"code":"input_too_large"`) || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) {
		t.Fatal("body size bound not actionable", w.Code, w.Body.String())
	}
	s.err = &identity.InputError{Field: "login", Reason: "invalid_format"}
	w = post(t, s, "/create", `{"login":"invalid login","password":"public synthetic password"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"login"`) || !strings.Contains(w.Body.String(), `"reason":"invalid_format"`) {
		t.Fatal("owning Service validation details lost", w.Body.String())
	}
}

func TestConfirmedCreateSurvivesPermissionHintFailure(t *testing.T) {
	s := &accountStub{account: identity.Account{ID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Login: "created.login", Enabled: true, Revision: 7}}
	r := httptest.NewRequest("POST", "/create", strings.NewReader(`{"login":"created.login","password":"public synthetic password"}`))
	r.Header.Set("Content-Type", "application/json")
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	v := view()
	v.CheckAllowed = func(string, string) (bool, error) { return false, achrix.ErrAuthorizationUnavailable }
	serve(s, w, r, v)
	var result result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(s.calls) != 1 || s.calls[0] != "create" || result.Account.ID != s.account.ID || result.Account.Login != s.account.Login || result.Warning != "permissions_unavailable" {
		t.Fatal("confirmed create was downgraded or identity lost", w.Code, w.Body.String(), s.calls)
	}
	for _, allowed := range result.Permissions {
		if allowed {
			t.Fatal("unverified permission hint granted")
		}
	}
}

func TestInputErrorPreservesSentinelWithoutRawTransportChain(t *testing.T) {
	r := httptest.NewRequest("POST", "/create", nil)
	err := decodeFailure(r, view(), "decode_body", bodyFault{true})
	if !errors.Is(err, identity.ErrUnavailable) || errors.Unwrap(err) != nil || strings.Contains(err.Error(), "private body") {
		t.Fatal("transport public identity or privacy changed", err)
	}
}

func TestServiceTimeoutDoesNotClaimNonExecution(t *testing.T) {
	s := &accountStub{err: context.DeadlineExceeded}
	w := post(t, s, "/create", `{"login":"known","password":"public synthetic password"}`)
	if w.Code != http.StatusServiceUnavailable || len(s.calls) != 1 || strings.Contains(w.Body.String(), "not_executed") {
		t.Fatal("post-dispatch timeout claims no mutation", w.Code, w.Body.String())
	}
}

type fixtureAuthenticator struct{}

func (fixtureAuthenticator) AuthenticateRequest(*http.Request, bool) (achrix.Principal, error) {
	return "operator", nil
}

type shellDeadlineWriter struct{ *deadlineWriter }

func (*shellDeadlineWriter) SetWriteDeadline(time.Time) error { return nil }

func TestBodyFaultKeepsOriginalProtectedCauseAcrossShellBoundary(t *testing.T) {
	service := &accountStub{}
	cause := bodyFault{true}
	var output bytes.Buffer
	var original error
	var operation, phase string
	s, err := shell.New(shell.Config{Origin: "https://admin.test", AuthPath: "/auth", Logger: slog.New(slog.NewJSONHandler(&output, nil)), Diagnostic: func(_ context.Context, op, stage string, err error) { operation, phase, original = op, stage, err }}, fixtureAuthenticator{}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return nil }), surface(service))
	if err != nil {
		t.Fatal(err)
	}
	defer s.FlushDiagnostics()
	r := httptest.NewRequest("POST", "https://admin.test/admin/identity/create", errorReader{cause})
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://admin.test")
	w := &shellDeadlineWriter{&deadlineWriter{ResponseRecorder: httptest.NewRecorder()}}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 408 || len(service.calls) != 0 || original != cause || operation != "identity.input" || phase != "decode_body" {
		t.Fatal("original body failure lost before protected boundary", w.Code, operation, phase, original)
	}
	if !strings.Contains(output.String(), "transport_timeout") || !strings.Contains(output.String(), "decode_body") || strings.Contains(output.String()+w.Body.String(), cause.Error()) {
		t.Fatal("diagnostics lack useful detail or expose private body cause", output.String(), w.Body.String())
	}
}
