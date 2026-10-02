// SPDX-License-Identifier: MPL-2.0
package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
)

type referenceModule struct{}

func (referenceModule) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "reference-test", Version: "1", Provides: []achrix.Capability{{ID: application.Read, Version: 1}}, Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}}}
}
func (referenceModule) Start(context.Context) error { return nil }
func (referenceModule) Ready(context.Context) error { return nil }
func (referenceModule) Stop(context.Context) error  { return nil }

type referenceRepository struct{ reads int }

func (*referenceRepository) Save(context.Context, domain.Note) error { return nil }
func (r *referenceRepository) Find(context.Context, string) (domain.Note, error) {
	r.reads++
	return domain.Note{}, domain.ErrNotFound
}

func TestBoundedResourceBeforePolicyAndUnavailableHTTPMapping(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var evaluations int
	a, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		evaluations++
		return errors.New("PRIVATE_POLICY_FAILURE")
	}), referenceModule{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	repository := &referenceRepository{}
	service := application.New(a, repository, logger)
	for _, id := range []string{"", "not-an-id", strings.Repeat("A", 10000)} {
		if _, err := service.Read(context.Background(), "reader", id); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if evaluations != 0 || repository.reads != 0 {
		t.Fatal("invalid reference reached policy/storage")
	}
	id := strings.Repeat("A", 26)
	if _, err := service.Read(context.Background(), "reader", id); !errors.Is(err, achrix.ErrAuthorizationUnavailable) || errors.Is(err, achrix.ErrDenied) {
		t.Fatal(err)
	}
	h := presentation.New(a, service, "fixture-writer", "fixture-reader", logger)
	r := httptest.NewRequest("GET", "/notes/"+id, nil)
	r.Header.Set("Authorization", "Bearer fixture-reader")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "unavailable") || strings.Contains(w.Body.String(), "PRIVATE_POLICY_FAILURE") {
		t.Fatal(w.Code, w.Body.String())
	}
	if evaluations != 2 || repository.reads != 0 {
		t.Fatal("evaluation failure accessed storage")
	}
}
