// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

type diagnosticBuffer struct {
	mu    sync.Mutex
	value bytes.Buffer
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.value.Write(p)
}
func (b *diagnosticBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.value.String() }

type capturedDiagnostic struct {
	operation, phase string
	cause            error
	ctx              context.Context
}
type correlationKey struct{}

func diagnosticShell(t *testing.T, auth Authenticator, policy Authorizer, surface Surface) (*Shell, *diagnosticBuffer, *[]capturedDiagnostic) {
	t.Helper()
	output := &diagnosticBuffer{}
	records := []capturedDiagnostic{}
	s, err := New(Config{Origin: "https://admin.test", AuthPath: "/auth",
		Logger: slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Diagnostic: func(ctx context.Context, operation, phase string, err error) {
			records = append(records, capturedDiagnostic{operation, phase, err, ctx})
		},
	}, auth, policy, surface)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.FlushDiagnostics)
	return s, output, &records
}

type deadlineFailureWriter struct {
	*transportRecorder
	readErr, writeErr error
}

func (w *deadlineFailureWriter) SetReadDeadline(d time.Time) error  { w.read = d; return w.readErr }
func (w *deadlineFailureWriter) SetWriteDeadline(d time.Time) error { w.write = d; return w.writeErr }

func TestTransportDiagnosticsPreservePhaseCauseAndTrustedContext(t *testing.T) {
	for _, phase := range []string{"set_read_deadline", "set_write_deadline"} {
		t.Run(phase, func(t *testing.T) {
			cause := fmt.Errorf("private transport credential: %w", http.ErrNotSupported)
			called := false
			s, output, records := diagnosticShell(t, &authentication{principal: "operator"}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) { called = true }))
			r := tlsRequest("POST", "/admin/accounts/change")
			r.Header.Set("Origin", "https://admin.test")
			r = r.WithContext(context.WithValue(r.Context(), correlationKey{}, "trusted-operation"))
			w := &deadlineFailureWriter{transportRecorder: &transportRecorder{ResponseRecorder: httptest.NewRecorder()}}
			if phase == "set_read_deadline" {
				w.readErr = cause
			} else {
				w.writeErr = cause
			}
			s.Handler().ServeHTTP(w, r)
			if called || w.Code != 503 || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) {
				t.Fatal("failed deadline admitted mutation", w.Code, w.Body.String())
			}
			if len(*records) != 1 || (*records)[0].phase != phase || (*records)[0].cause != cause || (*records)[0].ctx.Value(correlationKey{}) != "trusted-operation" {
				t.Fatal("phase, original cause or trusted correlation lost", records)
			}
			for _, value := range []string{output.String(), w.Body.String()} {
				if strings.Contains(value, "private transport credential") {
					t.Fatal("private transport message escaped protected sink")
				}
			}
			if !strings.Contains(output.String(), phase) || !strings.Contains(output.String(), "deadline_unsupported") {
				t.Fatal("safe diagnostic lacks actionable transport condition", output.String())
			}
		})
	}
}

type authorizerFunc func(context.Context, achrix.Principal, string, string) error

func (f authorizerFunc) Authorize(ctx context.Context, p achrix.Principal, capability, target string) error {
	return f(ctx, p, capability, target)
}

func TestPresentationPermissionSeparatesDeniedFromFailedPolicy(t *testing.T) {
	cause := errors.New("private policy credential")
	policy := authorizerFunc(func(_ context.Context, _ achrix.Principal, capability, _ string) error {
		switch capability {
		case "test.read":
			return nil
		case "test.denied":
			return achrix.ErrDenied
		default:
			return cause
		}
	})
	s, output, records := diagnosticShell(t, &authentication{principal: "operator"}, policy, testSurface(func(w http.ResponseWriter, _ *http.Request, view Request) {
		if allowed, err := view.CheckAllowed("test.denied", ""); allowed || err != nil {
			t.Error("denial conflated with failure")
		}
		if allowed, err := view.CheckAllowed("test.failed", ""); allowed || !errors.Is(err, achrix.ErrAuthorizationUnavailable) || errors.Unwrap(err) != nil {
			t.Error("provider failure lost safe machine category", err)
		}
		if view.Allowed("test.failed", "") {
			t.Error("compatibility hint granted after failure")
		}
		w.WriteHeader(204)
	}))
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 204 || len(*records) != 2 || (*records)[0].cause != cause || (*records)[1].cause != cause {
		t.Fatal("protected hook did not see repeated original policy causes", w.Code, records)
	}
	if strings.Contains(output.String(), "private policy credential") || !strings.Contains(output.String(), "presentation_permission") {
		t.Fatal("unsafe or unhelpful policy evidence", output.String())
	}
}

func TestRenderCauseIsProtectedAndPublicErrorHasNoPrivateChain(t *testing.T) {
	cause := errors.New("secret template provider token")
	page := template.Must(template.New("owned").Funcs(template.FuncMap{"fail": func() (string, error) { return "", cause }}).Parse(`{{define "content"}}{{fail}}{{end}}`))
	s, output, records := diagnosticShell(t, &authentication{principal: "operator"}, &authorization{}, testSurface(func(w http.ResponseWriter, _ *http.Request, view Request) {
		err := view.Render(Page{Template: page})
		if err == nil || errors.Unwrap(err) != nil || strings.Contains(err.Error(), cause.Error()) || !strings.Contains(err.Error(), "content_template") {
			t.Error("public render failure leaked or erased phase", err)
		}
		w.WriteHeader(503)
	}))
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 503 || w.Body.Len() != 0 || len(*records) != 1 || !errors.Is((*records)[0].cause, cause) {
		t.Fatal("render cause was lost or partial page was written", w.Code, records)
	}
	if strings.Contains(output.String(), cause.Error()) || !strings.Contains(output.String(), "content_template") {
		t.Fatal("render diagnostics are unsafe or empty", output.String())
	}
}

func TestAuthenticationAndLocaleFailuresRemainDiagnosable(t *testing.T) {
	cause := errors.New("private session provider detail")
	s, output, records := diagnosticShell(t, &authentication{err: cause}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	w := &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "authentication_unavailable") || len(*records) != 1 || (*records)[0].cause != cause {
		t.Fatal("provider failure became ordinary missing session")
	}
	s.config.SelectLanguage = func(*http.Request) string { return "uninstalled" }
	w = &transportRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/accounts"))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "locale_unavailable") || len(*records) != 2 || (*records)[1].phase != "select_locale" {
		t.Fatal("locale failure lost before mapping")
	}
	if strings.Contains(output.String(), cause.Error()) || !strings.Contains(output.String(), "unsupported_locale") {
		t.Fatal("ordinary locale/provider diagnostics unsafe or empty", output.String())
	}
}

func TestDistinctFailuresProtectedRepeatsAndIdleSummary(t *testing.T) {
	s, output, records := diagnosticShell(t, &authentication{}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	first, second := errors.New("secret cause one"), errors.New("secret cause two")
	for _, cause := range []error{first, first, second, second} {
		s.diagnose(context.Background(), "request", "provider", cause)
	}
	if len(*records) != 4 || (*records)[0].cause != first || (*records)[2].cause != second {
		t.Fatal("protected causes suppressed")
	}
	if strings.Count(output.String(), "admin operation failed") != 2 {
		t.Fatal("distinct causes hidden or repeated logs unbounded", output.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for strings.Count(output.String(), `"suppressed_records":1`) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Count(output.String(), `"suppressed_records":1`) != 2 || !strings.Contains(output.String(), "diagnostic_sequence") {
		t.Fatal("idle final summaries lost or uncorrelatable", output.String())
	}
	if strings.Contains(output.String(), "secret cause") {
		t.Fatal("cause material in ordinary summary")
	}
}

func TestDiagnosticCapacityReservesUnknownOutcomeAndStaleTimerCannotClearWindow(t *testing.T) {
	s, output, _ := diagnosticShell(t, &authentication{}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	for i := 0; i < 60; i++ {
		s.diagnose(context.Background(), "ready", "provider", fmt.Errorf("private detail %d", i))
	}
	s.diagnostics.mu.Lock()
	previous := s.diagnostics.generation
	s.diagnostics.mu.Unlock()
	s.diagnose(context.Background(), "identity.create", "mutation_outcome_unknown", errors.New("private uncertain commit"))
	if !strings.Contains(output.String(), "mutation_outcome_unknown") {
		t.Fatal("readiness noise hid uncertain mutation")
	}
	s.FlushDiagnostics()
	if !strings.Contains(output.String(), `"overflow_records":4`) || !strings.Contains(output.String(), `"sampled":true`) {
		t.Fatal("capacity overflow was silent", output.String())
	}
	s.diagnose(context.Background(), "request", "provider", errors.New("later window"))
	s.flushDiagnostics(previous, false)
	s.diagnostics.mu.Lock()
	retained := len(s.diagnostics.entries)
	s.diagnostics.mu.Unlock()
	if retained != 1 {
		t.Fatal("old fired timer cleared newer window")
	}
}

func TestLiveDependencyTimeoutDiffersFromCallerCancellation(t *testing.T) {
	s, output, records := diagnosticShell(t, &authentication{}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	s.diagnose(context.Background(), "request", "provider", fmt.Errorf("private timeout: %w", context.DeadlineExceeded))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.diagnose(ctx, "request", "provider", context.Canceled)
	if len(*records) != 2 || !strings.Contains(output.String(), "dependency_deadline") || !strings.Contains(output.String(), "caller_canceled") {
		t.Fatal("dependency failure and normal caller abort conflated", output.String())
	}
}

type failingResponseWriter struct {
	*transportRecorder
	writes int
}

func (w *failingResponseWriter) Write([]byte) (int, error) { w.writes++; return 0, io.ErrClosedPipe }

func TestResponseWriteFailureDoesNotAppendFallbackJSON(t *testing.T) {
	s, output, records := diagnosticShell(t, &authentication{}, &authorization{}, testSurface(func(http.ResponseWriter, *http.Request, Request) {}))
	w := &failingResponseWriter{transportRecorder: &transportRecorder{ResponseRecorder: httptest.NewRecorder()}}
	s.Handler().ServeHTTP(w, tlsRequest("GET", "/admin/login"))
	if w.writes != 1 || len(*records) != 1 || (*records)[0].phase != "response_write" || !errors.Is((*records)[0].cause, io.ErrClosedPipe) {
		t.Fatal("failed delivery retried with fallback or lost original cause", w.writes, records)
	}
	if !strings.Contains(output.String(), "closed_pipe") {
		t.Fatal("response write condition erased", output.String())
	}
}
