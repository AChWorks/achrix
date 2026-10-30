// SPDX-License-Identifier: MPL-2.0
package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/audit/remote"
	"github.com/AChWorks/achrix/bridge"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const httpResource = "https://product.example/api/audit"
const mcpResource = "https://product.example/mcp/audit"

// This declaration lets real Core policy reject requests before an unstarted
// real Audit module can touch storage. Successful persisted operations belong
// to the normal PostgreSQL Notes consumer, not a substituted Service here.
type permissionContract struct{}

func (permissionContract) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "fixture.audit-contract", Version: "test", Provides: []achrix.Capability{{ID: audit.Append, Version: 1}, {ID: audit.Query, Version: 1}, {ID: audit.Export, Version: 1}}}
}
func (permissionContract) Start(context.Context) error { return nil }
func (permissionContract) Ready(context.Context) error { return nil }
func (permissionContract) Stop(context.Context) error  { return nil }

type policyCall struct {
	actor             achrix.Principal
	operation, target string
}
type evidence struct {
	mu          sync.Mutex
	calls       []policyCall
	diagnostics []bridge.Diagnostic
}
type fixture struct {
	http, mcp *bridge.Endpoint
	proof     *evidence
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	proof := new(evidence)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, operation, target string) error {
		proof.mu.Lock()
		defer proof.mu.Unlock()
		proof.calls = append(proof.calls, policyCall{actor, operation, target})
		return achrix.ErrDenied
	}), permissionContract{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	module, err := audit.NewPostgres("host=localhost user=fixture dbname=fixture sslmode=disable", audit.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := module.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	service, err := audit.NewService(app, module)
	if err != nil {
		t.Fatal(err)
	}
	config := func(channel, resource string) bridge.Config {
		return bridge.Config{ResourceURL: resource, AuthorizationServers: []string{"https://issuer.example"}, Scopes: []string{"audit"}, Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			if token != "fixture."+channel {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: "reader", ClientID: "client-" + channel, ConnectionID: "connection-reader"}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"audit"}}, nil
		}, Admit: func(context.Context, bridge.Caller) error { return nil }, Diagnostic: func(_ context.Context, event bridge.Diagnostic) {
			proof.mu.Lock()
			defer proof.mu.Unlock()
			proof.diagnostics = append(proof.diagnostics, event)
		}}
	}
	h, err := remote.NewHTTP(service, config("http", httpResource))
	if err != nil {
		t.Fatal(err)
	}
	m, err := remote.NewMCP(service, config("mcp", mcpResource))
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.calls) != 0 || len(proof.diagnostics) != 0 {
		t.Fatal("constructors performed operation work")
	}
	return &fixture{h, m, proof}
}

func sendHTTP(t *testing.T, handler http.Handler, method, target, token string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, body)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Actor", "admin")
	r.Header.Set("X-Client-ID", "FORGED_CLIENT")
	r.Header.Set("X-Request-ID", "FORGED_REQUEST")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if id := w.Header().Get("X-Request-ID"); id == "" || id == "FORGED_REQUEST" {
		t.Fatal("request correlation was absent or caller-selected")
	}
	return w
}

func sendRPC(t *testing.T, handler http.Handler, token, method string, params map[string]any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	params["_meta"] = map[string]any{mcp.MetaKeyProtocolVersion: "2026-07-28", mcp.MetaKeyClientCapabilities: map[string]any{}, mcp.MetaKeyClientInfo: map[string]any{"name": "audit-contract-fixture", "version": "1"}}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, mcpResource, strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		r.Header.Set("Mcp-Name", name)
	}
	r.Header.Set("X-Actor", "admin")
	r.Header.Set("X-Client-ID", "FORGED_CLIENT")
	r.Header.Set("X-Request-ID", "FORGED_REQUEST")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		return w, nil
	}
	var envelope struct {
		Result json.RawMessage
		Error  json.RawMessage
	}
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || len(envelope.Error) > 0 || len(envelope.Result) == 0 {
		t.Fatal("unexpected SDK envelope", w.Body.String())
	}
	return w, envelope.Result
}

func toolFault(t *testing.T, result []byte) bridge.Fault {
	t.Helper()
	var output struct {
		IsError bool
		Content []struct{ Text string }
	}
	if json.Unmarshal(result, &output) != nil || !output.IsError || len(output.Content) != 1 {
		t.Fatal("expected a safe MCP failure", string(result))
	}
	var fault bridge.Fault
	if json.Unmarshal([]byte(output.Content[0].Text), &fault) != nil {
		t.Fatal("invalid safe fault", output)
	}
	return fault
}

func TestHTTPAndMCPUseVerifiedActorAndDistinctOwningPermissions(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ route, operation string }{{"/records", remote.QueryOperation}, {"/export", remote.ExportOperation}} {
		w := sendHTTP(t, f.http, "GET", httpResource+tc.route+"?target=ACCOUNT", "fixture.http", nil)
		var fault bridge.Fault
		if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &fault) != nil || fault.Code != "denied" || fault.Outcome != bridge.NotExecuted || fault.Reference != "" {
			t.Fatal("HTTP denial changed authority/effect evidence", w.Code, w.Body.String())
		}
		_, raw := sendRPC(t, f.mcp, "fixture.mcp", "tools/call", map[string]any{"name": tc.operation, "arguments": map[string]any{"target": "ACCOUNT"}})
		if failure := toolFault(t, raw); failure.Code != "denied" || failure.Outcome != bridge.NotExecuted {
			t.Fatal("MCP read did not reach ordinary denial", failure)
		}
	}
	f.proof.mu.Lock()
	if len(f.proof.calls) != 4 {
		t.Fatal("operation count changed", f.proof.calls)
	}
	for i, call := range f.proof.calls {
		want := audit.Query
		if i >= 2 {
			want = audit.Export
		}
		if call.actor != "reader" || call.target != "ACCOUNT" || call.operation != want {
			t.Fatal("forged authority or Query/Export substitution", call)
		}
	}
	for _, event := range f.proof.diagnostics {
		if event.Code == "denied" && (!event.Dispatched || event.Outcome != bridge.NotExecuted || event.Request.Caller.Principal != "reader" || event.Request.Caller.ClientID == "FORGED_CLIENT" || event.Request.ID == "FORGED_REQUEST" || !errors.Is(event.Cause, achrix.ErrDenied)) {
			t.Fatal("trusted denial diagnostics changed", event)
		}
	}
	f.proof.mu.Unlock()
	if w := sendHTTP(t, f.http, "GET", httpResource+"/records?target=ACCOUNT", "fixture.mcp", nil); w.Code != 401 {
		t.Fatal("MCP credential crossed the HTTP audience", w.Code)
	}
	if w, _ := sendRPC(t, f.mcp, "fixture.http", "tools/list", map[string]any{}); w.Code != 401 {
		t.Fatal("HTTP credential crossed the MCP audience", w.Code)
	}
}

func TestStrictAuditHTTPAndSDKInputsDoNotReachPolicy(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		method, suffix, body string
		status               int
	}{
		{"GET", "/records", "", 400}, {"GET", "/records?target=ACCOUNT&target=ACCOUNT", "", 400},
		{"GET", "/records?target=ACCOUNT&cursor=&cursor=", "", 400}, {"GET", "/records?target=ACCOUNT&actor=admin", "", 400},
		{"GET", "/records?target=ACCOUNT&attribution=x", "", 400}, {"GET", "/records?target=ACCOUNT&limit=0", "", 400},
		{"GET", "/records?target=ACCOUNT&limit=101", "", 400}, {"GET", "/records?target=ACCOUNT&limit=none", "", 400},
		{"GET", "/records?target=PRIVATE_TARGET/", "", 400}, {"GET", "/export?target=ACCOUNT&cursor=INVALID_PRIVATE_CURSOR", "", 400},
		{"GET", "/records?target=ACCOUNT&cursor=" + strings.Repeat("a", 513), "", 400},
		{"GET", "/records?target=ACCOUNT", `{"actor":"admin"}`, 400}, {"POST", "/records?target=ACCOUNT", "", 405},
		{"GET", "/records/?target=ACCOUNT", "", 400}, {"GET", "/append?target=ACCOUNT", "", 404},
	} {
		w := sendHTTP(t, f.http, tc.method, httpResource+tc.suffix, "fixture.http", strings.NewReader(tc.body))
		var fault bridge.Fault
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &fault) != nil || fault.Code == "" || fault.Outcome != bridge.NotExecuted || strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatal("invalid HTTP input lost safe bounded meaning", tc.suffix, w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET" {
			t.Fatal("wrong method lacked GET guidance")
		}
	}
	for _, args := range []map[string]any{{}, {"target": "ACCOUNT", "limit": 0}, {"target": "ACCOUNT", "limit": 101}, {"target": "ACCOUNT", "limit": nil}, {"target": "ACCOUNT", "actor": "admin"}, {"target": "ACCOUNT", "attribution": map[string]any{"client_id": "FORGED"}}, {"target": "PRIVATE_TARGET/"}} {
		_, raw := sendRPC(t, f.mcp, "fixture.mcp", "tools/call", map[string]any{"name": remote.QueryOperation, "arguments": args})
		if failure := toolFault(t, raw); failure.Code != "invalid_arguments" || failure.Outcome != bridge.NotExecuted || len(failure.Fields) == 0 {
			t.Fatal("SDK argument rejection changed", failure)
		}
	}
	f.proof.mu.Lock()
	defer f.proof.mu.Unlock()
	if len(f.proof.calls) != 0 {
		t.Fatal("invalid public arguments reached Core policy", f.proof.calls)
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var first, second any
	if err := json.Unmarshal(a, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &second); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(first, second)
}

func validateJSON(t *testing.T, schema []byte, value any) {
	t.Helper()
	var definition jsonschema.Schema
	if err := json.Unmarshal(schema, &definition); err != nil {
		t.Fatal(err)
	}
	resolved, err := definition.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatal("serialized DTO violates published schema", err, string(data))
	}
}

func TestPublishedSchemasMatchActualSDKCatalogAndDTOs(t *testing.T) {
	f := newFixture(t)
	var document struct {
		OpenAPI    string
		Components struct{ Schemas map[string]json.RawMessage }
		Paths      map[string]struct {
			Get struct {
				OperationID string `json:"operationId"`
				Parameters  []struct {
					Name   string
					Schema json.RawMessage
				}
			}
		}
	}
	if json.Unmarshal(remote.OpenAPI(), &document) != nil || document.OpenAPI != "3.1.1" || len(document.Paths) != 2 {
		t.Fatal("invalid published HTTP surface")
	}
	copy := remote.OpenAPI()
	copy[0] = '!'
	if remote.OpenAPI()[0] != '{' {
		t.Fatal("OpenAPI shared mutable backing bytes")
	}
	_, raw := sendRPC(t, f.mcp, "fixture.mcp", "tools/list", map[string]any{})
	var catalog struct {
		Tools []struct {
			Name         string
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
			Annotations  struct {
				ReadOnly    bool `json:"readOnlyHint"`
				Idempotent  bool `json:"idempotentHint"`
				Destructive bool `json:"destructiveHint"`
				OpenWorld   bool `json:"openWorldHint"`
			}
		}
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Tools) != 2 {
		t.Fatal("unexpected tool catalog", string(raw))
	}
	seen := map[string]bool{}
	for _, tool := range catalog.Tools {
		if (tool.Name != remote.QueryOperation && tool.Name != remote.ExportOperation) || seen[tool.Name] || !tool.Annotations.ReadOnly || !tool.Annotations.Idempotent || tool.Annotations.Destructive || tool.Annotations.OpenWorld {
			t.Fatal("unexpected tool/effect metadata", tool)
		}
		seen[tool.Name] = true
		if !sameJSON(t, tool.InputSchema, document.Components.Schemas["PageInput"]) || !sameJSON(t, tool.OutputSchema, document.Components.Schemas["Page"]) {
			t.Fatal("SDK schema drifted from HTTP publication", tool.Name)
		}
	}
	var input struct{ Properties map[string]json.RawMessage }
	if err := json.Unmarshal(document.Components.Schemas["PageInput"], &input); err != nil {
		t.Fatal(err)
	}
	for route, operation := range map[string]string{"/records": remote.QueryOperation, "/export": remote.ExportOperation} {
		path := document.Paths[route].Get
		if path.OperationID != operation || len(path.Parameters) != 3 {
			t.Fatal("HTTP binding drifted", route)
		}
		for _, parameter := range path.Parameters {
			if !sameJSON(t, parameter.Schema, input.Properties[parameter.Name]) {
				t.Fatal("HTTP input fragment differs", parameter.Name)
			}
		}
	}
	record := remote.RecordInfo{ID: "abcdefghijklmnopqrstuvwxyz", Actor: "operator", Action: "identity.account.set-enabled", Target: "ACCOUNT", Authority: "achrix.identity.account.set-enabled", Outcome: "succeeded", OccurredAt: "2026-10-30T08:00:00.123456Z"}
	validateJSON(t, document.Components.Schemas["RecordInfo"], record)
	record.Attribution = &remote.AttributionInfo{ClientID: "https://client.example/id", Channel: "mcp", RequestID: "REQUEST_1", Operation: "achrix.identity.account.set-enabled"}
	validateJSON(t, document.Components.Schemas["RecordInfo"], record)
	validateJSON(t, document.Components.Schemas["AttributionInfo"], record.Attribution)
	validateJSON(t, document.Components.Schemas["Page"], remote.Page{Records: []remote.RecordInfo{record}})
	validateJSON(t, document.Components.Schemas["Page"], remote.Page{Records: []remote.RecordInfo{}})
}
