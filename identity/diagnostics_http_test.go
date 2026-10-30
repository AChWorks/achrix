// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type diagnosticBody struct{ failure error }

func (b diagnosticBody) Read([]byte) (int, error) { return 0, b.failure }
func (b diagnosticBody) Close() error             { return nil }
func TestBodyTimeoutIsDiagnosedBeforeMutation(t *testing.T) {
	cause := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	request := webProofRequest(http.MethodPost, "/login", "")
	request.Body = diagnosticBody{cause}
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

var _ io.ReadCloser = diagnosticBody{}
