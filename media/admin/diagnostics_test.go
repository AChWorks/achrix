// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/media"
)

type bodyTimeout struct{}

func (*bodyTimeout) Error() string   { return "private-body-transport-token" }
func (*bodyTimeout) Timeout() bool   { return true }
func (*bodyTimeout) Temporary() bool { return true }

type bodyFailureReader struct{ cause error }

func (r bodyFailureReader) Read([]byte) (int, error) { return 0, r.cause }

type bodyDataFailureReader struct {
	data  []byte
	cause error
}

func (r *bodyDataFailureReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.cause
	}
	return n, nil
}

func TestBodyIOFailuresRetainOriginalCauseBeforeService(t *testing.T) {
	const private = "private-body-io-token"
	const part = "--owned-boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"one.png\"\r\n\r\nPNG"
	const value = `{"id":"` + testID + `","revision":"2"}`
	for _, failure := range []struct {
		name, reason string
		cause        error
	}{
		{"closed_pipe", "closed_pipe", fmt.Errorf("%s: %w", private, io.ErrClosedPipe)},
		{"reset", "dependency_failure", &net.OpError{Op: "read", Net: "tcp", Err: fmt.Errorf("%s: %w", private, syscall.ECONNRESET)}},
		{"joined_eof", "closed_pipe", errors.Join(io.EOF, fmt.Errorf("%s: %w", private, io.ErrClosedPipe))},
		{"joined_input", "closed_pipe", errors.Join(media.ErrInput, fmt.Errorf("%s: %w", private, io.ErrClosedPipe))},
	} {
		for _, scenario := range []struct {
			name, path, contentType, prefix, phase string
			together                               bool
		}{
			{"json_first", "delete", "application/json", "", "json_body", false},
			{"json_trailing", "delete", "application/json", value, "json_trailing", false},
			{"multipart_part", "upload", "multipart/form-data; boundary=owned-boundary", "", "upload_part", false},
			{"multipart_body", "upload", "multipart/form-data; boundary=owned-boundary", part, "upload_body", false},
			{"multipart_trailing", "upload", "multipart/form-data; boundary=owned-boundary", part + "\r\n--owned-boundary\r\n", "upload_trailing", false},
			{"json_bytes_and_error", "delete", "application/json", value, "json_body", true},
			{"multipart_bytes_and_error", "upload", "multipart/form-data; boundary=owned-boundary", part + "\r\n--owned-boundary--\r\n", "upload_part", true},
		} {
			t.Run(failure.name+"/"+scenario.name, func(t *testing.T) {
				s := &libraryStub{}
				var ordinary bytes.Buffer
				calls := 0
				app, err := shell.New(shell.Config{Origin: "https://example.test", AuthPath: "/identity", Logger: slog.New(slog.NewJSONHandler(&ordinary, nil)), Diagnostic: func(ctx context.Context, operation, phase string, cause error) {
					calls++
					if operation != "media" || phase != scenario.phase || cause != failure.cause || ctx.Value(diagnosticCorrelation{}) != "trusted-body-operation" {
						t.Error("original read cause/phase/correlation lost", operation, phase, cause)
					}
				}}, diagnosticAuthenticator{}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return nil }), surface(s))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(app.FlushDiagnostics)
				var body io.Reader = io.MultiReader(strings.NewReader(scenario.prefix), bodyFailureReader{failure.cause})
				if scenario.together {
					body = &bodyDataFailureReader{data: []byte(scenario.prefix), cause: failure.cause}
				}
				r := httptest.NewRequest("POST", "https://example.test/admin/media/"+scenario.path, body)
				r = r.WithContext(context.WithValue(r.Context(), diagnosticCorrelation{}, "trusted-body-operation"))
				r.Header.Set("Origin", "https://example.test")
				r.Header.Set("Content-Type", scenario.contentType)
				w := &transport{ResponseRecorder: httptest.NewRecorder()}
				app.Handler().ServeHTTP(w, r)
				if w.Code != http.StatusServiceUnavailable || len(s.calls) != 0 || calls != 1 || !strings.Contains(w.Body.String(), `"code":"unavailable"`) || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) {
					t.Fatal("operational read failure became input or reached Service", w.Code, w.Body.String(), s.calls, calls)
				}
				if !strings.Contains(ordinary.String(), `"phase":"`+scenario.phase+`"`) || !strings.Contains(ordinary.String(), `"reason":"`+failure.reason+`"`) {
					t.Fatal("safe diagnostic evidence missing", ordinary.String())
				}
				if strings.Contains(w.Body.String()+ordinary.String(), private) {
					t.Fatal("private body failure text escaped")
				}
			})
		}
	}
}

func TestParserAndSizeFailuresRemainSeparateFromBodyIO(t *testing.T) {
	const prefix = "--owned-boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"one.png\"\r\n\r\n"
	for _, scenario := range []struct {
		name, path, contentType, body string
		status                        int
	}{
		{"json_syntax", "delete", "application/json", `{"id":`, 400},
		{"json_type", "delete", "application/json", `{"id":"` + testID + `","revision":2}`, 400},
		{"json_unknown_field", "delete", "application/json", `{"id":"` + testID + `","revision":"2","private-body-value":true}`, 400},
		{"json_trailing", "delete", "application/json", `{"id":"` + testID + `","revision":"2"} {}`, 400},
		{"json_size", "delete", "application/json", `{"id":"` + strings.Repeat("x", 2048) + `"}`, 413},
		{"multipart_header", "upload", "multipart/form-data; boundary=owned-boundary", "--owned-boundary\r\ninvalid header\r\n", 400},
		{"multipart_framing", "upload", "multipart/form-data; boundary=owned-boundary", prefix + "PNG", 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := &libraryStub{}
			r := httptest.NewRequest("POST", "/"+scenario.path, strings.NewReader(scenario.body))
			r.Header.Set("Content-Type", scenario.contentType)
			w := &transport{ResponseRecorder: httptest.NewRecorder()}
			handlerFor(s).serve(w, r, requestView())
			if w.Code != scenario.status || len(s.calls) != 0 || strings.Contains(w.Body.String(), "private-body-value") || strings.Contains(w.Body.String(), `"code":"unavailable"`) {
				t.Fatal("parser/size control changed", w.Code, w.Body.String(), s.calls)
			}
		})
	}
}

func TestJSONTimeoutBeforeOrAfterValueNeverReachesService(t *testing.T) {
	for _, prefix := range []string{"", `{"id":"` + testID + `","revision":"2"}`} {
		s := &libraryStub{}
		r := httptest.NewRequest("POST", "/delete", io.MultiReader(strings.NewReader(prefix), bodyFailureReader{&bodyTimeout{}}))
		r.Header.Set("Content-Type", "application/json")
		w := &transport{ResponseRecorder: httptest.NewRecorder()}
		handlerFor(s).serve(w, r, requestView())
		if w.Code != http.StatusRequestTimeout || len(s.calls) != 0 || !strings.Contains(w.Body.String(), `"code":"request_timeout"`) || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) || strings.Contains(w.Body.String(), "private-body") {
			t.Fatal("body timeout became invalid input or reached mutation", w.Code, w.Body.String(), s.calls)
		}
	}
}

func TestMultipartTimeoutHasNonExecutionEvidence(t *testing.T) {
	s := &libraryStub{}
	r := httptest.NewRequest("POST", "/upload", bodyFailureReader{&bodyTimeout{}})
	r.Header.Set("Content-Type", "multipart/form-data; boundary=owned-boundary")
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, r, requestView())
	if w.Code != http.StatusRequestTimeout || len(s.calls) != 0 || !strings.Contains(w.Body.String(), `"outcome":"not_executed"`) {
		t.Fatal(w.Code, w.Body.String(), s.calls)
	}
}

func TestCompletedUploadSurvivesPermissionPresentationFailure(t *testing.T) {
	s := &libraryStub{asset: sample()}
	view := requestView()
	view.CheckAllowed = func(string, string) (bool, error) { return false, errors.New("private-policy-dependency") }
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	handlerFor(s).serve(w, multipartRequest(t, "one.png", []byte("PNG"), ""), view)
	var got item
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(s.calls) != 1 || s.calls[0] != "create" || got.Asset.ID != testID || got.Warning != "permissions_unavailable" || got.Permissions["read"] || got.Permissions["delete"] || strings.Contains(w.Body.String(), "private-policy") {
		t.Fatal("confirmed upload falsified or permission failure exposed", w.Code, w.Body.String(), s.calls)
	}
}

func TestPermissionFailureAndExplicitDenialHaveDifferentPresentation(t *testing.T) {
	for _, dependencyFailure := range []bool{false, true} {
		s := &libraryStub{page: media.Page{Assets: []media.Asset{sample()}}}
		view := requestView()
		view.Localizer = presentationLocalizer(t, "en")
		view.CheckAllowed = func(string, string) (bool, error) {
			if dependencyFailure {
				return false, achrix.ErrAuthorizationUnavailable
			}
			return false, nil
		}
		w := httptest.NewRecorder()
		view.Render = func(p shell.Page) error { return p.Template.ExecuteTemplate(w, "content", p.Data) }
		handlerFor(s).serve(w, httptest.NewRequest("GET", "/", nil), view)
		body := w.Body.String()
		if dependencyFailure {
			if !strings.Contains(body, "Permission checks are temporarily unavailable") || strings.Contains(body, "Download or deletion of this file is not permitted") {
				t.Fatal("policy failure presented as denial", body)
			}
		} else if !strings.Contains(body, "Download or deletion of this file is not permitted") || strings.Contains(body, "Permission checks are temporarily unavailable") {
			t.Fatal("denial became outage", body)
		}
	}
}

type diagnosticAuthenticator struct{}

func (diagnosticAuthenticator) AuthenticateRequest(*http.Request, bool) (achrix.Principal, error) {
	return "operator", nil
}

type diagnosticCorrelation struct{}

func TestShellCapturesOriginalBodyCauseWithTrustedContext(t *testing.T) {
	s := &libraryStub{}
	var ordinary, support bytes.Buffer
	cause := &bodyTimeout{}
	captured := false
	app, err := shell.New(shell.Config{Origin: "https://example.test", AuthPath: "/identity", Logger: slog.New(slog.NewJSONHandler(&ordinary, nil)), Diagnostic: func(ctx context.Context, operation, phase string, err error) {
		if operation != "media" || phase != "json_body" {
			t.Error("wrong diagnostic owner/phase", operation, phase)
		}
		if err != cause || ctx.Value(diagnosticCorrelation{}) != "trusted-operation" {
			t.Error("original cause/correlation lost", err)
		}
		captured = true
		// This support export retains the authorized fault kind and correlation,
		// not the provider's secret-bearing Error() string.
		fmt.Fprintf(&support, "operation=%s phase=%s cause_type=%T correlation=%s", operation, phase, err, ctx.Value(diagnosticCorrelation{}))
	}}, diagnosticAuthenticator{}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return nil }), surface(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.FlushDiagnostics)
	r := httptest.NewRequest("POST", "https://example.test/admin/media/delete", bodyFailureReader{cause})
	r = r.WithContext(context.WithValue(r.Context(), diagnosticCorrelation{}, "trusted-operation"))
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("Content-Type", "application/json")
	w := &transport{ResponseRecorder: httptest.NewRecorder()}
	app.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestTimeout || !captured || len(s.calls) != 0 {
		t.Fatal("real shell lost transport diagnosis", w.Code, w.Body.String(), captured)
	}
	for _, evidence := range []string{ordinary.String(), support.String(), w.Body.String()} {
		if strings.Contains(evidence, "private-body-transport-token") {
			t.Fatal("private text retained")
		}
	}
	if !strings.Contains(ordinary.String(), "json_body") || !strings.Contains(support.String(), "trusted-operation") {
		t.Fatal("diagnosis/export deleted useful details", ordinary.String(), support.String())
	}
}

func TestPartialRenderedPageDoesNotAcquireJSONFallback(t *testing.T) {
	s := &libraryStub{}
	view := requestView()
	view.Localizer = presentationLocalizer(t, "en")
	w := httptest.NewRecorder()
	view.Render = func(shell.Page) error { _, _ = w.Write([]byte("<html>partial")); return shell.ErrResponseDelivery }
	handlerFor(s).serve(w, httptest.NewRequest("GET", "/", nil), view)
	if w.Body.String() != "<html>partial" {
		t.Fatal("JSON appended after partial page", w.Body.String())
	}
}
