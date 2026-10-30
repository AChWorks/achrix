// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
const httpResource = "https://product.example/api/identity/v1"
const mcpResource = "https://product.example/mcp/identity"

// These parser/catalog cases deliberately stop before the concrete Service.
// The normally pinned Notes consumer executes real PostgreSQL HTTP/SDK calls.
func testConfig(resource string, calls *atomic.Int32) bridge.Config {
	return bridge.Config{ResourceURL: resource, AuthorizationServers: []string{"https://issuer.example"}, Scopes: []string{"identity"},
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			if token != "fixture-token" {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: "operator", ClientID: "fixture-client"}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"identity"}}, nil
		}, Admit: func(context.Context, bridge.Caller) error { return nil },
		Gate: func(context.Context, bridge.Caller, string) error { calls.Add(1); return achrix.ErrDenied }, Diagnostic: func(context.Context, bridge.Diagnostic) {},
	}
}

func rpc(t *testing.T, e http.Handler, method string, params map[string]any) json.RawMessage {
	t.Helper()
	params["_meta"] = map[string]any{mcp.MetaKeyProtocolVersion: "2026-07-28", mcp.MetaKeyClientCapabilities: map[string]any{}, mcp.MetaKeyClientInfo: map[string]any{"name": "identity-contract", "version": "1"}}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, mcpResource, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer fixture-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		r.Header.Set("Mcp-Name", name)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	var result struct{ Result, Error json.RawMessage }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Error) != 0 || len(result.Result) == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	return result.Result
}

func TestStrictHTTPInputAndExactRevision(t *testing.T) {
	for _, raw := range []string{"1", "9007199254740993", "9223372036854775807"} {
		r := httptest.NewRequest("POST", httpResource+"/accounts/"+testID+"/enabled", strings.NewReader(`{"expected_revision":"`+raw+`","enabled":false}`))
		r.Header.Set("Content-Type", "application/json")
		v, err := decodeEnabled(r, testID)
		if err != nil || v.ExpectedRevision != raw || v.Enabled || v.AccountID != testID {
			t.Fatal("exact false/revision lost", v, err)
		}
	}
	for _, body := range []string{
		`{}`, `{"expected_revision":"1"}`, `{"expected_revision":"1","enabled":null}`, `{"expected_revision":"1","enabled":"false"}`,
		`{"expected_revision":9007199254740993,"enabled":false}`, `{"expected_revision":"01","enabled":false}`, `{"expected_revision":"0","enabled":false}`, `{"expected_revision":"9223372036854775808","enabled":false}`,
		`{"expected_revision":"1","enabled":true,"\u0065nabled":false}`, `{"expected_revision":"1","enabled":true,"actor":"operator"}`,
		`{"expected_revision":"1","enabled":true} {}`, `[]`, `null`, `{"expected_revision":"1","enabled":true`,
	} {
		r := httptest.NewRequest("POST", httpResource+"/accounts/"+testID+"/enabled", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeEnabled(r, testID)
		var f *bridge.Fault
		if !errors.As(err, &f) || f.Outcome != bridge.NotExecuted || len(f.Fields) == 0 {
			t.Fatal("invalid body admitted or lost safe guidance", body, err)
		}
	}
	var gates atomic.Int32
	e, err := NewHTTP(new(identity.Service), testConfig(httpResource, &gates))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/accounts/" + testID + "?actor=operator", "", 400},
		{"GET", "/accounts/" + testID, "x", 400},
		{"PUT", "/accounts/" + testID, "", 405},
		{"POST", "/accounts/" + testID + "/enabled", strings.Repeat(" ", 4096) + `{}`, 413},
		{"POST", "/accounts/" + testID + "/enabled", `{"expected_revision":"1","enabled":false,"request_id":"forged"}`, 400},
		{"GET", "/accounts/" + testID + "/extra", "", 404},
	} {
		r := httptest.NewRequest(tc.method, httpResource+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer fixture-token")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		var f bridge.Fault
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &f) != nil || f.Outcome != bridge.NotExecuted {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	if gates.Load() != 0 {
		t.Fatal("invalid HTTP input reached admission", gates.Load())
	}
}

func TestHTTPRejectsAmbiguousContentEncodingBeforeOperation(t *testing.T) {
	var gates atomic.Int32
	e, err := NewHTTP(new(identity.Service), testConfig(httpResource, &gates))
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range [][]string{{"gzip"}, {"", "gzip"}, {"gzip", ""}, {"", ""}} {
		r := httptest.NewRequest(http.MethodPost, httpResource+"/accounts/"+testID+"/enabled", strings.NewReader(`{"expected_revision":"1","enabled":false}`))
		r.Header.Set("Authorization", "Bearer fixture-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header["Content-Encoding"] = values
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		var f bridge.Fault
		if w.Code != http.StatusBadRequest || json.Unmarshal(w.Body.Bytes(), &f) != nil || f.Code != "invalid_arguments" || f.Outcome != bridge.NotExecuted || f.Fields["content_type"] == "" {
			t.Fatal(values, w.Code, w.Body.String())
		}
	}
	if gates.Load() != 0 {
		t.Fatal("ambiguous encoding reached operation gate", gates.Load())
	}
}

func TestPublishedSDKCatalogAndNoSecretSurface(t *testing.T) {
	var calls atomic.Int32
	e, err := NewMCP(new(identity.Service), testConfig(mcpResource, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("idle construction performed operation work")
	}
	var catalog struct {
		Tools []struct {
			Name                      string
			InputSchema, OutputSchema json.RawMessage
			Annotations               struct{ ReadOnlyHint, IdempotentHint, DestructiveHint, OpenWorldHint bool }
		}
	}
	if err := json.Unmarshal(rpc(t, e, "tools/list", map[string]any{}), &catalog); err != nil || len(catalog.Tools) != 2 {
		t.Fatal("unexpected Identity catalog", err)
	}
	s, err := publishedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{AccountOperation: {"AccountInput", "AccountInfo"}, SetEnabledOperation: {"SetEnabledInput", "SetEnabledResult"}}
	for _, tool := range catalog.Tools {
		pair, ok := want[tool.Name]
		if !ok {
			t.Fatal("unexpected secret or management tool", tool.Name)
		}
		delete(want, tool.Name)
		for i, schema := range []json.RawMessage{tool.InputSchema, tool.OutputSchema} {
			var a, b any
			if json.Unmarshal(schema, &a) != nil || json.Unmarshal(s[pair[i]], &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatal("SDK/public schema drift", tool.Name)
			}
		}
		read := tool.Name == AccountOperation
		if tool.Annotations.ReadOnlyHint != read || tool.Annotations.IdempotentHint != read || tool.Annotations.DestructiveHint == read || tool.Annotations.OpenWorldHint {
			t.Fatal("incorrect effect annotations", tool.Name)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing explicit tool")
	}
	for name, value := range map[string]any{"AccountInfo": AccountInfo{ID: testID, Login: "example-user", Enabled: false, Revision: "9007199254740993"}, "SetEnabledResult": SetEnabledResult{AccountID: testID, Outcome: "succeeded"}} {
		var schema jsonschema.Schema
		if err := json.Unmarshal(s[name], &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(v); err != nil {
			t.Fatal("serialized DTO violates published contract", name, err)
		}
	}
	for _, args := range []map[string]any{
		{"account_id": testID, "expected_revision": "1"},
		{"account_id": testID, "expected_revision": int64(9007199254740993), "enabled": false},
		{"account_id": testID, "expected_revision": "01", "enabled": false},
		{"account_id": testID, "expected_revision": "1", "enabled": false, "client_id": "forged"},
		{"account_id": testID, "expected_revision": "1", "enabled": nil},
	} {
		var result struct {
			IsError bool
			Content []struct{ Text string }
		}
		if json.Unmarshal(rpc(t, e, "tools/call", map[string]any{"name": SetEnabledOperation, "arguments": args}), &result) != nil || !result.IsError || len(result.Content) != 1 {
			t.Fatal("invalid MCP input admitted")
		}
		var f bridge.Fault
		if json.Unmarshal([]byte(result.Content[0].Text), &f) != nil || f.Code != "invalid_arguments" || f.Outcome != bridge.NotExecuted {
			t.Fatal("unsafe MCP input failure", result)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid schema reached operation gate")
	}
	var doc struct {
		OpenAPI string
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
		}
	}
	if err := json.Unmarshal(OpenAPI(), &doc); err != nil || doc.OpenAPI != "3.1.1" || len(doc.Paths) != 2 || doc.Paths["/accounts/{account_id}"]["get"].OperationID != AccountOperation || doc.Paths["/accounts/{account_id}/enabled"]["post"].OperationID != SetEnabledOperation {
		t.Fatal("HTTP published mapping differs", err)
	}
	copy := OpenAPI()
	copy[0] = '!'
	if OpenAPI()[0] != '{' {
		t.Fatal("external mutation changed schema")
	}
}

func TestOutcomeAndDirectInvocationEvidence(t *testing.T) {
	private := errors.New("PRIVATE_DEPENDENCY")
	for _, err := range []error{identity.ErrUnavailable, identity.ErrConflict, identity.ErrLimited, context.DeadlineExceeded} {
		if f := operationFault(err, false, ""); f.Outcome != bridge.NotExecuted || f.Reference != "" {
			t.Fatal("read error invented a state-changing effect", f)
		}
	}
	unknown := operationFault(errors.Join(identity.ErrUnavailable, private), true, testID)
	if unknown.Outcome != bridge.Unknown || unknown.Code != "identity_unavailable" || unknown.Reference != testID || !errors.Is(unknown.Cause, private) {
		t.Fatal("uncertainty or protected cause lost", unknown)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		f := operationFault(err, true, testID)
		if f.Outcome != bridge.Unknown || f.Reference != "" {
			t.Fatal("cancellation promoted an unverified target", f)
		}
	}
	for _, err := range []error{achrix.ErrDenied, achrix.ErrAuthorizationUnavailable, achrix.ErrNotReady, &identity.InputError{Field: "id", Reason: "invalid_format"}} {
		f := operationFault(err, true, testID)
		if f.Outcome != bridge.NotExecuted || f.Reference != "" {
			t.Fatal("pre-effect failure manufactured a reference", f)
		}
	}
	for _, call := range []func() error{
		func() error {
			_, err := account(new(identity.Service))(context.Background(), AccountInput{testID})
			return err
		},
		func() error {
			_, err := setEnabled(new(identity.Service))(context.Background(), SetEnabledInput{testID, "1", false})
			return err
		},
	} {
		var f *bridge.Fault
		if !errors.As(call(), &f) || f.Code != "unauthenticated" || f.Outcome != bridge.NotExecuted {
			t.Fatal("direct adapter call invented trusted request context")
		}
	}
}
