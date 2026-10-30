// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
)

func TestRecordProjectionPreservesTruthfulAttributionAbsence(t *testing.T) {
	record := audit.Record{ID: "abcdefghijklmnopqrstuvwxyz", Actor: "operator", Action: "identity.account.set-enabled", Target: "ACCOUNT", Authority: "achrix.identity.account.set-enabled", Outcome: "succeeded", OccurredAt: time.Date(2026, 10, 30, 9, 0, 0, 123456000, time.FixedZone("fixture", 3600))}
	view := present(record)
	if view.OccurredAt != "2026-10-30T08:00:00.123456Z" || view.Attribution != nil {
		t.Fatal("legacy time/provenance changed", view)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "attribution") || strings.Contains(string(data), "sequence") {
		t.Fatal("legacy output invented provenance or internal sequence", string(data))
	}
	record.Attribution = &audit.Attribution{ClientID: "https://client.example/id", Channel: "mcp", RequestID: "REQUEST_1", Operation: "achrix.identity.account.set-enabled"}
	view = present(record)
	record.Attribution.ClientID = "changed-source-pointer"
	data, err = json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if view.Attribution == nil || view.Attribution.ClientID != "https://client.example/id" || strings.Contains(string(data), "connection_id") || strings.Contains(string(data), "ClientID") {
		t.Fatal("attribution projection lost snapshot or wire field contract", string(data))
	}
}

func TestReadFaultsRetainCausesAndNeverClaimMutation(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   string
		status int
	}{
		{achrix.ErrDenied, "denied", 403},
		{achrix.ErrAuthorizationUnavailable, "authorization_unavailable", 503},
		{audit.ErrLimited, "busy", 429},
		{audit.ErrUnavailable, "audit_unavailable", 503},
		{achrix.ErrNotReady, "audit_unavailable", 503},
		{context.Canceled, "deadline_exceeded", 503},
		{context.DeadlineExceeded, "deadline_exceeded", 503},
	} {
		f := serviceFault(tc.err)
		if f.Code != tc.code || f.HTTPStatus != tc.status || f.Outcome != bridge.NotExecuted || f.Reference != "" || !errors.Is(f.Cause, tc.err) {
			t.Fatal("read failure changed evidence", f)
		}
	}
	var service audit.Service
	_, source := service.Query(context.Background(), "operator", "PRIVATE_INVALID_TARGET/", "", 20)
	f := serviceFault(source)
	if f.Code != "invalid_arguments" || f.Fields["target"] == "" || f.Fields["reason"] != "invalid_reference" {
		t.Fatal("owning input rule lost its safe field/reason", f)
	}
	private := errors.New("PRIVATE_AUDIT_PROVIDER")
	f = serviceFault(private)
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(f.Cause, private) || strings.Contains(string(data), private.Error()) {
		t.Fatal("protected cause was lost or exposed", string(data))
	}
}

func TestUnverifiedHandlerContextCannotReachOwningService(t *testing.T) {
	called := false
	handler := readPage(func(context.Context, achrix.Principal, string, string, int) (audit.Page, error) {
		called = true
		return audit.Page{}, nil
	})
	_, err := handler(context.Background(), PageInput{Target: "ACCOUNT"})
	var f *bridge.Fault
	if called || !errors.As(err, &f) || f.Code != "unauthenticated" || f.Outcome != bridge.NotExecuted {
		t.Fatal("unverified direct invocation reached Service", err)
	}
}
