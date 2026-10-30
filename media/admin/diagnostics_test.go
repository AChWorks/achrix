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
	"net/http"
	"net/http/httptest"
	"strings"
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
		if operation != "media" || phase != "request_body" {
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
	if !strings.Contains(ordinary.String(), "request_body") || !strings.Contains(support.String(), "trusted-operation") {
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
