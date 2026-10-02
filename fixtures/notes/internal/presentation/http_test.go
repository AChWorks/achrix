// SPDX-License-Identifier: MPL-2.0
package presentation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type testModule struct{ readiness error }

func (m *testModule) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "notes-test", Version: "1", Provides: []achrix.Capability{{ID: application.Create, Version: 1}, {ID: application.Read, Version: 1}}}
}
func (*testModule) Start(context.Context) error   { return nil }
func (m *testModule) Ready(context.Context) error { return m.readiness }
func (*testModule) Stop(context.Context) error    { return nil }

type testRepository struct{ reads, writes int }

func (r *testRepository) Save(context.Context, domain.Note) error {
	r.writes++
	return domain.ErrUnavailable
}
func (r *testRepository) Find(context.Context, string) (domain.Note, error) {
	r.reads++
	return domain.Note{}, domain.ErrNotFound
}

func TestRepeatedRejectionsHaveBoundedSafeEdgeVisibility(t *testing.T) {
	var output strings.Builder
	logger := diagnosis.New(&output)
	repository := &testRepository{}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), &testModule{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown(context.Background())
	output.Reset()
	h := presentation.New(app, application.New(app, repository, logger), "PRIVATE_WRITER_TOKEN", "PRIVATE_READER_TOKEN", logger)
	started := time.Now()
	ids := map[string]bool{}
	for i := range 1000 {
		for _, tc := range []struct {
			token, body, content string
			status               int
		}{
			{fmt.Sprintf("PRIVATE_INVALID_TOKEN_%d", i), "PRIVATE_PAYLOAD", "application/json", 401},
			{"PRIVATE_READER_TOKEN", `{"text":"PRIVATE_PAYLOAD"}`, "application/json", 403},
			{"PRIVATE_WRITER_TOKEN", "PRIVATE_PAYLOAD", "application/json", 400},
			{"PRIVATE_WRITER_TOKEN", "PRIVATE_PAYLOAD", "text/plain", 415},
		} {
			r := httptest.NewRequest("POST", "/notes", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", tc.content)
			r.Header.Set("X-Request-ID", "PRIVATE_UNTRUSTED_CORRELATION")
			r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1000", i%256)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			id := w.Header().Get("X-Request-ID")
			if w.Code != tc.status || id == "" || id == "PRIVATE_UNTRUSTED_CORRELATION" || ids[id] {
				t.Fatal("public semantics or trusted correlation changed", w.Code, tc.status, id)
			}
			ids[id] = true
			if strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatal("private error response")
			}
		}
	}
	if repository.reads != 0 || repository.writes != 0 {
		t.Fatal("rejected traffic accessed persistence")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	bound := int(time.Since(started)/time.Second) + 1
	if len(lines) > bound {
		t.Fatalf("%d edge records exceeded elapsed-time bound %d", len(lines), bound)
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		id, _ := record["request_id"].(string)
		if record["level"] != "INFO" || record["component"] != "notes.http" || record["operation"] != "request" || !ids[id] {
			t.Fatal("missing safe bounded edge attribution", record)
		}
	}
	if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), "192.0.2.") || strings.Contains(output.String(), "achrix.authorization") {
		t.Fatal("secret/payload or duplicate Core record in default diagnostics")
	}
}

func TestUnavailableAndReadinessRemainBoundedAndFailClosed(t *testing.T) {
	var output strings.Builder
	logger := diagnosis.New(&output)
	repository := &testRepository{}
	dependency := &testModule{readiness: errors.New("PRIVATE_DEPENDENCY")}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		return errors.New("PRIVATE_POLICY_ERROR")
	}), dependency)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown(context.Background())
	output.Reset()
	h := presentation.New(app, application.New(app, repository, logger), "PRIVATE_WRITER_TOKEN", "PRIVATE_READER_TOKEN", logger)
	started := time.Now()
	for range 1000 {
		for _, path := range []string{"/ready", "/notes/" + strings.Repeat("A", 26)} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("Authorization", "Bearer PRIVATE_READER_TOKEN")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 503 || w.Header().Get("X-Request-ID") == "" || strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatal("unavailable/probe response changed", w.Code, w.Body.String())
			}
		}
	}
	if repository.reads != 0 || repository.writes != 0 {
		t.Fatal("unavailable policy accessed persistence")
	}
	if count, bound := strings.Count(output.String(), "\n"), int(time.Since(started)/time.Second)+1; count == 0 || count > bound {
		t.Fatalf("abnormal request visibility count=%d bound=%d", count, bound)
	}
	if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatal("raw failure or duplicate dependency detail logged")
	}
}
