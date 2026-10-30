// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBodyTimeoutIsDiagnosedBeforeMutation(t *testing.T) {
	cause := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	request := webProofRequest(http.MethodPost, "/login", "")
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	// The HTTP server can cancel the request context after a transport timeout.
	// That secondary cancellation must not erase the actual timeout category.
	request.Body = &diagnosticSequenceBody{steps: []diagnosticReadStep{{"", cause}}, beforeRead: cancel}
	var protected error
	var target any
	err := decodeWebDiagnostic(newDeadlineRecorder(), request, &target, func(phase string, failure error) {
		if phase != "read" {
			t.Error("wrong phase")
		}
		protected = failure
	})
	if !errors.Is(err, ErrBodyTimeout) || !errors.Is(err, ErrUnavailable) || protected != cause {
		t.Fatal("body timeout lost safe identity or protected cause")
	}
	writer := httptest.NewRecorder()
	webError(writer, err)
	if writer.Code != http.StatusRequestTimeout || !strings.Contains(writer.Body.String(), `"code":"body_timeout"`) || !strings.Contains(writer.Body.String(), `"outcome":"not_executed"`) {
		t.Fatal("pre-service timeout missing truthful response")
	}
	writer = httptest.NewRecorder()
	webError(writer, ErrUnavailable)
	if writer.Code != http.StatusServiceUnavailable || strings.Contains(writer.Body.String(), "not_executed") {
		t.Fatal("generic unavailability implies safe replay")
	}
}
func TestIdentityManagementInputFieldsAreSafe(t *testing.T) {
	service := &Service{}
	for _, tc := range []struct {
		field string
		run   func() error
	}{
		{"login", func() error {
			_, e := service.LookupAccount(context.Background(), "actor", "PRIVATE INVALID LOGIN")
			return e
		}},
		{"id", func() error {
			return service.SetPassword(context.Background(), "actor", "PRIVATE INVALID ID", 1, "PRIVATE PASSWORD")
		}},
		{"expected_revision", func() error {
			return service.SetEnabled(context.Background(), "actor", strings.Repeat("A", 26), 0, true)
		}},
	} {
		err := tc.run()
		var input *InputError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &input) || input.Field != tc.field || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("input field missing or private value echoed", err)
		}
	}
}

type diagnosticWriteFailure struct {
	*deadlineRecorder
	failure error
}

func (w *diagnosticWriteFailure) Write([]byte) (int, error) { return 0, w.failure }
func TestPostgresResponseFailureDoesNotUndoCommittedLogin(t *testing.T) {
	f := newIdentityFixture(t)
	f.create(t, "response-user")
	web, err := NewWeb(f.service, webProofOrigin)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("private response path WRITE_SECRET")
	var captured error
	f.module.config.Diagnostic = func(_ context.Context, operation, phase string, failure error) {
		if operation == "session_response" && phase == "write_response" {
			captured = failure
		}
	}
	writer := &diagnosticWriteFailure{newDeadlineRecorder(), cause}
	request := webProofRequest(http.MethodPost, "/login", `{"login":"response-user","password":"`+testPassword+`"}`)
	web.Handler().ServeHTTP(writer, request)
	var count int
	if err = f.db.QueryRow(context.Background(), "SELECT count(*) FROM identity.sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || captured != cause || !strings.Contains(f.logs.String(), `"outcome":"committed"`) || strings.Contains(f.logs.String(), "WRITE_SECRET") {
		t.Fatal("successful mutation became failure or lost delivery diagnosis")
	}
}

type diagnosticReadStep struct {
	text    string
	failure error
}
type diagnosticSequenceBody struct {
	steps      []diagnosticReadStep
	beforeRead func()
	reads      int
	closed     bool
}

func (b *diagnosticSequenceBody) Read(p []byte) (int, error) {
	b.reads++
	if b.beforeRead != nil {
		b.beforeRead()
	}
	if len(b.steps) == 0 {
		return 0, io.EOF
	}
	step := &b.steps[0]
	n := copy(p, step.text)
	step.text = step.text[n:]
	if step.text != "" {
		return n, nil
	}
	err := step.failure
	b.steps = b.steps[1:]
	return n, err
}
func (b *diagnosticSequenceBody) Close() error { b.closed = true; return nil }

func TestWebBodyReadFailuresRemainOperationalAndPrivate(t *testing.T) {
	const document = `{"login":"fixture","password":"BODY_SECRET long fixture password"}`
	reset := &net.OpError{Op: "read", Net: "tcp", Err: fmt.Errorf("BODY_SECRET: %w", syscall.ECONNRESET)}
	closed := fmt.Errorf("BODY_SECRET: %w", io.ErrClosedPipe)
	canceled := fmt.Errorf("BODY_SECRET: %w", context.Canceled)
	timeout := fmt.Errorf("BODY_SECRET: %w", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded})
	joinedEOF := errors.Join(io.EOF, reset)
	parserLike := errors.New(`json: unknown field "BODY_SECRET"`)
	for _, tc := range []struct {
		name, phase, reason string
		steps               []diagnosticReadStep
		cause               error
		status              int
	}{
		{"first reset", "read", "connection_reset", []diagnosticReadStep{{"", reset}}, reset, http.StatusServiceUnavailable},
		{"trailing closed pipe", "read_trailing", "dependency_failure", []diagnosticReadStep{{document, nil}, {"", closed}}, closed, http.StatusServiceUnavailable},
		{"complete document with error", "read", "connection_reset", []diagnosticReadStep{{document, reset}}, reset, http.StatusServiceUnavailable},
		{"joined EOF with error", "read", "connection_reset", []diagnosticReadStep{{document, joinedEOF}}, joinedEOF, http.StatusServiceUnavailable},
		{"trailing joined EOF with error", "read_trailing", "connection_reset", []diagnosticReadStep{{document, nil}, {"", joinedEOF}}, joinedEOF, http.StatusServiceUnavailable},
		{"live dependency cancellation", "read", "dependency_canceled", []diagnosticReadStep{{"", canceled}}, canceled, http.StatusServiceUnavailable},
		{"parser-looking I/O message", "read", "dependency_failure", []diagnosticReadStep{{"", parserLike}}, parserLike, http.StatusServiceUnavailable},
		{"trailing timeout", "read_trailing", "dependency_timeout", []diagnosticReadStep{{document, nil}, {"", timeout}}, timeout, http.StatusRequestTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs diagnosticLog
			var protected error
			ctx := context.WithValue(context.Background(), diagnosticContextKey{}, "trusted-body-request")
			m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Now: time.Now, Diagnostic: func(got context.Context, operation, phase string, cause error) {
				if got.Value(diagnosticContextKey{}) != "trusted-body-request" || operation != "http_body" || phase != tc.phase {
					t.Error("body failure lost request context or failing phase")
				}
				protected = cause
			}}}
			t.Cleanup(m.flushDiagnostics)
			web, err := NewWeb(&Service{module: m}, webProofOrigin)
			if err != nil {
				t.Fatal(err)
			}
			newBody := func() *diagnosticSequenceBody {
				return &diagnosticSequenceBody{steps: append([]diagnosticReadStep(nil), tc.steps...)}
			}
			request := webProofRequest(http.MethodPost, "/login", "").WithContext(ctx)
			body := newBody()
			request.Body = body
			writer := newDeadlineRecorder()
			// There is deliberately no Application: dispatching Login would fail
			// this test instead of silently invoking any authentication work.
			web.Handler().ServeHTTP(writer, request)
			code := "authentication_unavailable"
			if tc.status == http.StatusRequestTimeout {
				code = "body_timeout"
			}
			if writer.Code != tc.status || !strings.Contains(writer.Body.String(), `"code":"`+code+`"`) || !strings.Contains(writer.Body.String(), `"outcome":"not_executed"`) || writer.Header().Get("Set-Cookie") != "" || len(web.slots) != 0 || !body.closed {
				t.Fatal("body I/O did not stop before Service dispatch", writer.Code, writer.Body.String())
			}
			if protected != tc.cause || m.FailureCount() != 1 || !strings.Contains(logs.String(), `"phase":"`+tc.phase+`"`) || !strings.Contains(logs.String(), `"reason":"`+tc.reason+`"`) || !strings.Contains(logs.String(), `"cause_type":"`+fmt.Sprintf("%T", tc.cause)+`"`) || !strings.Contains(logs.String(), `"outcome":"not_executed"`) {
				t.Fatal("body failure lost original cause or useful ordinary evidence", logs.String())
			}
			request = webProofRequest(http.MethodPost, "/login", "").WithContext(ctx)
			request.Body = newBody()
			var target any
			err = decodeWeb(newDeadlineRecorder(), request, &target)
			var provider *net.OpError
			var internal *diagnosticError
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrInvalid) || errors.Is(err, tc.cause) || errors.As(err, &provider) || errors.As(err, &internal) {
				t.Fatal("public body failure lost its category or exposed a provider chain")
			}
			if strings.Contains(writer.Body.String()+logs.String()+fmt.Sprintf("%v %#v", err, err), "BODY_SECRET") {
				t.Fatal("body failure leaked private content")
			}
		})
	}
}

func TestWebCallerAbortRetainsProtectedCauseWithoutDependencyError(t *testing.T) {
	for _, alreadyCanceled := range []bool{true, false} {
		t.Run(fmt.Sprintf("already_canceled=%t", alreadyCanceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), diagnosticContextKey{}, "trusted-canceled-request"))
			defer cancel()
			cause := fmt.Errorf("BODY_SECRET: %w", context.Canceled)
			body := &diagnosticSequenceBody{steps: []diagnosticReadStep{{"", cause}}}
			phase, expected := "read", cause
			if alreadyCanceled {
				cancel()
				phase, expected = "admission", context.Canceled
			} else {
				body.beforeRead = cancel
			}
			var logs diagnosticLog
			var protected error
			m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Now: time.Now, Diagnostic: func(got context.Context, operation, gotPhase string, failure error) {
				if got.Value(diagnosticContextKey{}) != "trusted-canceled-request" || got.Err() != context.Canceled || operation != "http_body" || gotPhase != phase {
					t.Error("caller abort lost context or phase")
				}
				protected = failure
			}}}
			t.Cleanup(m.flushDiagnostics)
			web, err := NewWeb(&Service{module: m}, webProofOrigin)
			if err != nil {
				t.Fatal(err)
			}
			request := webProofRequest(http.MethodPost, "/login", "").WithContext(ctx)
			request.Body = body
			var target any
			err = web.decodeWeb(newDeadlineRecorder(), request, &target)
			writer := httptest.NewRecorder()
			webError(writer, err)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrInvalid) || protected != expected || logs.Len() != 0 || m.FailureCount() != 0 {
				t.Fatal("caller abort became invalid input or dependency failure, or lost its protected cause")
			}
			if alreadyCanceled && body.reads != 0 {
				t.Fatal("already-aborted caller reached Body.Read")
			}
			if writer.Code != http.StatusServiceUnavailable || !strings.Contains(writer.Body.String(), `"outcome":"not_executed"`) || strings.Contains(writer.Body.String()+err.Error(), "BODY_SECRET") {
				t.Fatal("caller abort public result is unsafe or implies a mutation")
			}
		})
	}
}

func TestWebMalformedJSONIsNotAnOperationalReadFailure(t *testing.T) {
	for _, tc := range []struct{ name, document string }{
		{"empty", ""},
		{"syntax", `{"value":}`},
		{"truncated JSON", `{"value":"BODY_SECRET`},
		{"wrong type", `{"value":1}`},
		{"unknown field", `{"value":"ok","BODY_SECRET":true}`},
		{"trailing value", `{"value":"ok"} {"BODY_SECRET":true}`},
		{"size", `{"value":"` + strings.Repeat("BODY_SECRET", 300) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs diagnosticLog
			captures := 0
			m := &Module{logger: slog.New(slog.NewJSONHandler(&logs, nil)), config: Config{Diagnostic: func(context.Context, string, string, error) { captures++ }}}
			t.Cleanup(m.flushDiagnostics)
			web, err := NewWeb(&Service{module: m}, webProofOrigin)
			if err != nil {
				t.Fatal(err)
			}
			request := webProofRequest(http.MethodPost, "/login", tc.document)
			var target struct {
				Value string `json:"value"`
			}
			err = web.decodeWeb(newDeadlineRecorder(), request, &target)
			writer := httptest.NewRecorder()
			webError(writer, err)
			if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnavailable) || captures != 0 || logs.Len() != 0 || m.FailureCount() != 0 || writer.Code != http.StatusBadRequest || !strings.Contains(writer.Body.String(), `"code":"invalid_input"`) || strings.Contains(writer.Body.String()+err.Error(), "BODY_SECRET") {
				t.Fatal("malformed JSON became an operational failure or leaked its content")
			}
		})
	}
}
