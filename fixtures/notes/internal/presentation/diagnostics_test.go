// SPDX-License-Identifier: MPL-2.0
package presentation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
)

type outcomeRepository struct {
	cause  error
	writes int
	id     string
}

func (r *outcomeRepository) Save(_ context.Context, note domain.Note) error {
	r.writes++
	r.id = note.ID
	return r.cause
}
func (*outcomeRepository) Find(context.Context, string) (domain.Note, error) {
	return domain.Note{}, domain.ErrNotFound
}

type brokenBody struct{ cause error }

func (b brokenBody) Read([]byte) (int, error) { return 0, b.cause }
func (brokenBody) Close() error               { return nil }

type bytesAndFailure struct {
	data  []byte
	cause error
}

func (b *bytesAndFailure) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, b.cause
}
func (*bytesAndFailure) Close() error { return nil }

type brokenResponse struct {
	*httptest.ResponseRecorder
	cause error
}

func (w brokenResponse) Write([]byte) (int, error) { return 0, w.cause }

func TestWriteOutcomeAndTransportDiagnosticsRemainTruthful(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cause   error
		outcome string
		status  int
	}{
		{"deadline_after_dispatch", fmt.Errorf("PRIVATE_WRITE: %w", context.DeadlineExceeded), "unknown", 503},
		{"not_executed", domain.ErrNotExecuted, "not_executed", 503},
		{"rejected", domain.ErrNotCommitted, "not_committed", 503},
		{"success", nil, "", 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			logger := diagnosis.New(&logs)
			defer diagnosis.Flush(logger)
			app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), &testModule{})
			if err != nil {
				t.Fatal(err)
			}
			if err = app.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer app.Shutdown(context.Background())
			repository := &outcomeRepository{cause: tc.cause}
			var captured error
			service := application.NewWithDiagnostics(app, repository, logger, func(_ context.Context, operation, phase string, cause error) {
				if operation != application.Create || phase != "repository_save" {
					t.Fatal(operation, phase)
				}
				captured = cause
			})
			handler := presentation.New(app, service, "writer", "reader", logger)
			request := httptest.NewRequest("POST", "/notes", strings.NewReader(`{"text":"PRIVATE_NOTE"}`))
			request.Header.Set("Authorization", "Bearer writer")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || repository.writes != 1 {
				t.Fatal(response.Code, repository.writes, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.outcome != "" && body["outcome"] != tc.outcome {
				t.Fatal(body)
			}
			if tc.outcome == "unknown" && (body["id"] != repository.id || captured != tc.cause || body["next_action"] == nil) {
				t.Fatal("lost reconciliation identity/cause/action", body)
			}
			if tc.status != 201 && (strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(logs.String(), "PRIVATE_")) {
				t.Fatal("private diagnostic escaped")
			}
		})
	}
}

func TestBodyTimeoutAndFailedSuccessResponsePreservePhaseAndCause(t *testing.T) {
	var logs strings.Builder
	logger := diagnosis.New(&logs)
	defer diagnosis.Flush(logger)
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), &testModule{})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown(context.Background())
	repository := &outcomeRepository{}
	phases := map[string]error{}
	handler := presentation.NewWithDiagnostics(app, application.New(app, repository, logger), "writer", "reader", logger, func(_ context.Context, operation, phase string, cause error) {
		if operation != "request" {
			t.Fatal(operation)
		}
		phases[phase] = cause
	})
	timeout := fmt.Errorf("PRIVATE_BODY: %w", context.DeadlineExceeded)
	request := httptest.NewRequest("POST", "/notes", nil)
	request.Body = brokenBody{timeout}
	request.Header.Set("Authorization", "Bearer writer")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 408 || repository.writes != 0 || phases["decode_body"] != timeout || !strings.Contains(response.Body.String(), `"outcome":"not_executed"`) {
		t.Fatal(response.Code, response.Body.String(), phases)
	}
	request = httptest.NewRequest("POST", "/notes", strings.NewReader(`{"text":"PRIVATE_TEXT"}`))
	request.Header.Set("Authorization", "Bearer writer")
	request.Header.Set("Content-Type", "application/json")
	writeErr := fmt.Errorf("PRIVATE_DESTINATION: %w", io.ErrClosedPipe)
	broken := brokenResponse{httptest.NewRecorder(), writeErr}
	handler.ServeHTTP(broken, request)
	if broken.Code != 201 || repository.writes != 1 || phases["write_response"] != writeErr {
		t.Fatal("success was erased or response cause lost")
	}
	diagnosis.Flush(logger)
	if !strings.Contains(logs.String(), `"phase":"write_response"`) || !strings.Contains(logs.String(), `"outcome":"committed"`) || strings.Contains(logs.String(), "PRIVATE_") {
		t.Fatal(logs.String())
	}
}

func TestFieldCorrectionDoesNotEchoInput(t *testing.T) {
	// Domain callers and HTTP adapters share the same bounded, source-owned detail.
	err := domain.ValidateText(strings.Repeat("PRIVATE_", 50))
	var detail *domain.InputError
	if !errors.Is(err, domain.ErrInvalid) || !errors.As(err, &detail) || detail.Field != "text" || !strings.Contains(detail.Problem, "200") || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatal(err)
	}
}

func TestBodyIOFailureRetainedBeforeInputMappingOrDispatch(t *testing.T) {
	type trustedKey struct{}
	for _, tc := range []struct {
		name, prefix      string
		cause             error
		together, aborted bool
		status            int
		reason            string
	}{
		{name: "initial_closed_pipe", cause: fmt.Errorf("PRIVATE_READ: %w", io.ErrClosedPipe), status: 503, reason: "closed_pipe"},
		{name: "trailing_closed_pipe", prefix: `{"text":"PRIVATE_NOTE"}`, cause: fmt.Errorf("PRIVATE_READ: %w", io.ErrClosedPipe), status: 503, reason: "closed_pipe"},
		{name: "bytes_with_error", prefix: `{"text":"PRIVATE_NOTE"}`, cause: fmt.Errorf("PRIVATE_READ: %w", io.ErrClosedPipe), together: true, status: 503, reason: "closed_pipe"},
		{name: "joined_eof", prefix: `{"text":"PRIVATE_NOTE"}`, cause: errors.Join(io.EOF, fmt.Errorf("PRIVATE_READ: %w", io.ErrClosedPipe)), together: true, status: 503, reason: "closed_pipe"},
		{name: "live_dependency_cancel", cause: fmt.Errorf("PRIVATE_READ: %w", context.Canceled), status: 503, reason: "canceled"},
		{name: "caller_abort", cause: fmt.Errorf("PRIVATE_READ: %w", context.Canceled), aborted: true, status: 408, reason: "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			logger := diagnosis.New(&logs)
			defer diagnosis.Flush(logger)
			app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), &testModule{})
			if err != nil {
				t.Fatal(err)
			}
			if err = app.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer app.Shutdown(context.Background())
			repository := &outcomeRepository{}
			var captured error
			handler := presentation.NewWithDiagnostics(app, application.New(app, repository, logger), "writer", "reader", logger, func(ctx context.Context, operation, phase string, cause error) {
				if operation != "request" || phase != "decode_body" || ctx.Value(trustedKey{}) != "trusted-parent" {
					t.Fatal("lost phase/context", operation, phase)
				}
				captured = cause
			})
			request := httptest.NewRequest("POST", "/notes", nil)
			request.Body = io.NopCloser(io.MultiReader(strings.NewReader(tc.prefix), brokenBody{tc.cause}))
			if tc.together {
				request.Body = &bytesAndFailure{[]byte(tc.prefix), tc.cause}
			}
			ctx := context.WithValue(request.Context(), trustedKey{}, "trusted-parent")
			if tc.aborted {
				aborted, cancel := context.WithCancel(ctx)
				cancel()
				ctx = aborted
			}
			request = request.WithContext(ctx)
			request.Header.Set("Authorization", "Bearer writer")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Request-ID", "PRIVATE_UNTRUSTED_ID")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			diagnosis.Flush(logger)
			expectedCause := tc.cause
			if tc.aborted {
				expectedCause = context.Canceled
			}
			if response.Code != tc.status || repository.writes != 0 || captured != expectedCause || !strings.Contains(response.Body.String(), `"outcome":"not_executed"`) {
				t.Fatal("I/O failure lost or dispatched", response.Code, response.Body.String(), repository.writes)
			}
			if !tc.aborted && (!strings.Contains(logs.String(), `"phase":"decode_body"`) || !strings.Contains(logs.String(), `"reason":"`+tc.reason+`"`) || !strings.Contains(logs.String(), response.Header().Get("X-Request-ID"))) {
				t.Fatal("local operational evidence missing", logs.String())
			}
			if tc.aborted && strings.Contains(logs.String(), `"level":"ERROR"`) {
				t.Fatal("caller abort misclassified as dependency alarm")
			}
			if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(logs.String(), "PRIVATE_") {
				t.Fatal("raw body/transport data escaped")
			}
		})
	}
}

func TestMalformedBodiesRemainCallerCorrections(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"text":7}`, 400}, {`{"unexpected":"PRIVATE_FIELD"}`, 400}, {`{"text":`, 400}, {`{"text":"ok"} {}`, 400}, {strings.Repeat(" ", 1025), 413},
	} {
		var logs strings.Builder
		logger := diagnosis.New(&logs)
		// A malformed request must never need an Application/Service instance.
		captures := 0
		handler := presentation.NewWithDiagnostics(nil, nil, "writer", "reader", logger, func(context.Context, string, string, error) { captures++ })
		request := httptest.NewRequest("POST", "/notes", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer writer")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		diagnosis.Flush(logger)
		if response.Code != tc.status || captures != 0 || strings.Contains(response.Body.String(), "PRIVATE_") {
			t.Fatal(response.Code, response.Body.String(), captures)
		}
	}
}
