// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testHTTPResource = "https://product.example/api/media"
	testMCPResource  = "https://product.example/mcp/media"
)

// Catalog/parser proofs need no storage. Gate deliberately prevents every
// otherwise valid invocation from reaching this unstarted concrete Service.
// Actual PostgreSQL operations and socket deadlines are covered by the normal
// Notes consumer and Bridge streaming tests respectively.
func contractConfig(resource string, verifications, gates *atomic.Int32) bridge.Config {
	return bridge.Config{
		ResourceURL: resource, AuthorizationServers: []string{"https://issuer.example"}, Scopes: []string{"media"},
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			verifications.Add(1)
			if token != "fixture-credential" {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: "reader", ClientID: "fixture-client", ConnectionID: "fixture-connection"}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"media"}}, nil
		},
		Admit:      func(context.Context, bridge.Caller) error { return nil },
		Gate:       func(context.Context, bridge.Caller, string) error { gates.Add(1); return achrix.ErrDenied },
		Diagnostic: func(context.Context, bridge.Diagnostic) {},
	}
}

func catalogRPC(t *testing.T, endpoint http.Handler, method string, params map[string]any) json.RawMessage {
	t.Helper()
	params["_meta"] = map[string]any{
		mcp.MetaKeyProtocolVersion: "2026-07-28", mcp.MetaKeyClientCapabilities: map[string]any{},
		mcp.MetaKeyClientInfo: map[string]any{"name": "media-contract-fixture", "version": "1"},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, testMCPResource, strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer fixture-credential")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		r.Header.Set("Mcp-Name", name)
	}
	w := httptest.NewRecorder()
	endpoint.ServeHTTP(w, r)
	var envelope struct {
		Result json.RawMessage
		Error  json.RawMessage
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || len(envelope.Error) > 0 || len(envelope.Result) == 0 {
		t.Fatal("unexpected SDK protocol result", w.Code, w.Body.String())
	}
	return envelope.Result
}

func equalJSON(t *testing.T, first, second []byte) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(first, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

func validateSchema(t *testing.T, schema []byte, value any) {
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
		t.Fatal("published schema rejected serialized DTO", err, string(data))
	}
}

func TestPublishedSchemasMatchActualSDKCatalogAndHTTPBindings(t *testing.T) {
	var document struct {
		OpenAPI    string                                       `json:"openapi"`
		Components struct{ Schemas map[string]json.RawMessage } `json:"components"`
		Paths      map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name   string
				Schema json.RawMessage
			}
		} `json:"paths"`
	}
	if err := json.Unmarshal(OpenAPI(), &document); err != nil || document.OpenAPI != "3.1.1" || len(document.Paths) != 5 {
		t.Fatal("invalid OpenAPI surface", err)
	}
	copy := OpenAPI()
	copy[0] = '!'
	if OpenAPI()[0] != '{' {
		t.Fatal("published schema was externally mutable")
	}
	var verifications, gates atomic.Int32
	endpoint, err := NewMCP(new(media.Service), contractConfig(testMCPResource, &verifications, &gates), testHTTPResource)
	if err != nil {
		t.Fatal(err)
	}
	if verifications.Load() != 0 || gates.Load() != 0 {
		t.Fatal("constructor performed admission work")
	}
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
	if err := json.Unmarshal(catalogRPC(t, endpoint, "tools/list", map[string]any{}), &catalog); err != nil || len(catalog.Tools) != 4 {
		t.Fatal("unexpected Media tool catalog", err)
	}
	mapping := map[string][2]string{ListOperation: {"ListInput", "Page"}, StatusOperation: {"StatusInput", "AssetInfo"}, DeleteOperation: {"DeleteInput", "DeleteResult"}, ReconcileOperation: {"ReconcileInput", "ReconcileResult"}}
	seen := map[string]bool{}
	for _, tool := range catalog.Tools {
		pair, known := mapping[tool.Name]
		if !known || seen[tool.Name] {
			t.Fatal("unexpected or duplicate tool", tool.Name)
		}
		seen[tool.Name] = true
		if !equalJSON(t, tool.InputSchema, document.Components.Schemas[pair[0]]) || !equalJSON(t, tool.OutputSchema, document.Components.Schemas[pair[1]]) {
			t.Fatal("actual SDK schema drifted from publication", tool.Name)
		}
		read := tool.Name == ListOperation || tool.Name == StatusOperation
		if tool.Annotations.ReadOnly != read || tool.Annotations.Idempotent != read || tool.Annotations.Destructive == read || tool.Annotations.OpenWorld {
			t.Fatal("tool effect metadata overclaimed", tool.Name, tool.Annotations)
		}
	}
	bindings := map[string]map[string]string{
		"/assets":                    {"get": ListOperation, "post": CreateOperation},
		"/assets/{asset_id}":         {"get": StatusOperation},
		"/assets/{asset_id}/content": {"get": ReadOperation},
		"/assets/{asset_id}/delete":  {"post": DeleteOperation},
		"/reconcile":                 {"post": ReconcileOperation},
	}
	for route, methods := range bindings {
		if len(document.Paths[route]) != len(methods) {
			t.Fatal("unexpected HTTP methods", route)
		}
		for method, operation := range methods {
			if document.Paths[route][method].OperationID != operation {
				t.Fatal("HTTP operation identity drifted", route, method)
			}
		}
	}
	for route, input := range map[string]string{"/assets": "ListInput", "/assets/{asset_id}": "StatusInput", "/assets/{asset_id}/content": "StatusInput"} {
		var shape struct{ Properties map[string]json.RawMessage }
		if err := json.Unmarshal(document.Components.Schemas[input], &shape); err != nil {
			t.Fatal(err)
		}
		for _, parameter := range document.Paths[route]["get"].Parameters {
			if !equalJSON(t, parameter.Schema, shape.Properties[parameter.Name]) {
				t.Fatal("HTTP input drifted from typed MCP fragment", route, parameter.Name)
			}
		}
	}
	var body, tool struct{ Properties map[string]json.RawMessage }
	if json.Unmarshal(document.Components.Schemas["DeleteBody"], &body) != nil || json.Unmarshal(document.Components.Schemas["DeleteInput"], &tool) != nil || !equalJSON(t, body.Properties["expected_revision"], tool.Properties["expected_revision"]) {
		t.Fatal("delete precondition differs by channel")
	}
	resource, err := referenceResource(testHTTPResource)
	if err != nil {
		t.Fatal(err)
	}
	asset := sampleAsset()
	for _, state := range []string{"pending", "ready", "deleting", "deleted"} {
		asset.State = state
		validateSchema(t, document.Components.Schemas["AssetInfo"], present(asset, resource, true))
	}
	asset.State = "ready"
	validateSchema(t, document.Components.Schemas["Page"], Page{Assets: []AssetInfo{present(asset, nil, false)}})
	validateSchema(t, document.Components.Schemas["Page"], Page{Assets: []AssetInfo{}})
	validateSchema(t, document.Components.Schemas["DeleteResult"], DeleteResult{testAssetID, "succeeded"})
	validateSchema(t, document.Components.Schemas["ReconcileResult"], ReconcileResult{2, 1})
	var assetSchema struct {
		Properties map[string]struct{ Enum []string }
	}
	if err := json.Unmarshal(document.Components.Schemas["AssetInfo"], &assetSchema); err != nil {
		t.Fatal(err)
	}
	mimes := make([]string, 0)
	for _, format := range media.SupportedFormats() {
		mimes = append(mimes, format.MIME)
	}
	if !reflect.DeepEqual(assetSchema.Properties["mime"].Enum, mimes) {
		t.Fatal("published retained MIME catalog differs from the owning module")
	}
}

func TestSDKRejectsUnpublishedArgumentsBeforeGate(t *testing.T) {
	var verifications, gates atomic.Int32
	endpoint, err := NewMCP(new(media.Service), contractConfig(testMCPResource, &verifications, &gates), testHTTPResource)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		operation string
		args      map[string]any
	}{
		{ListOperation, map[string]any{"limit": 0}},
		{ListOperation, map[string]any{"limit": 101}},
		{ListOperation, map[string]any{"limit": nil}},
		{StatusOperation, map[string]any{"asset_id": "PRIVATE_INVALID_ID"}},
		{StatusOperation, map[string]any{"asset_id": testAssetID, "actor": "admin"}},
		{DeleteOperation, map[string]any{"asset_id": testAssetID, "expected_revision": int64(9007199254740993)}},
		{DeleteOperation, map[string]any{"asset_id": testAssetID, "expected_revision": "01"}},
		{ReconcileOperation, map[string]any{"limit": 41}},
		{ReconcileOperation, map[string]any{"limit": nil}},
	} {
		var result struct {
			IsError bool
			Content []struct{ Type, Text string }
		}
		if json.Unmarshal(catalogRPC(t, endpoint, "tools/call", map[string]any{"name": tc.operation, "arguments": tc.args}), &result) != nil || !result.IsError || len(result.Content) != 1 {
			t.Fatal("invalid arguments escaped SDK validation", tc.operation, result)
		}
		var failure bridge.Fault
		if json.Unmarshal([]byte(result.Content[0].Text), &failure) != nil || failure.Code != "invalid_arguments" || failure.Outcome != bridge.NotExecuted || len(failure.Fields) == 0 || strings.Contains(result.Content[0].Text, "PRIVATE_INVALID_ID") {
			t.Fatal("SDK invalid argument lacks safe correction", result)
		}
	}
	if gates.Load() != 0 {
		t.Fatal("invalid schema reached operation admission", gates.Load())
	}
}

type deadlineRecorder struct{ *httptest.ResponseRecorder }

func (*deadlineRecorder) SetReadDeadline(time.Time) error  { return nil }
func (*deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

func TestHTTPFixedRoutesAndKnownEmptyBodyFailBeforeGate(t *testing.T) {
	var verifications, gates atomic.Int32
	endpoint, err := NewHTTP(new(media.Service), contractConfig(testHTTPResource, &verifications, &gates))
	if err != nil {
		t.Fatal(err)
	}
	if verifications.Load() != 0 || gates.Load() != 0 {
		t.Fatal("HTTP construction performed admission work")
	}
	for _, tc := range []struct {
		method, path, body, kind, disposition string
		status                                int
	}{
		{"GET", "/outside", "", "", "", 404},
		{"GET", "/assets/", "", "", "", 400},
		{"PUT", "/assets", "", "", "", 405},
		{"POST", "/assets", "", "application/octet-stream", `attachment; filename="empty.txt"`, 400},
		{"GET", "/assets?cursor=&cursor=", "", "", "", 400},
		{"POST", "/assets/" + testAssetID + "/delete", `{"expected_revision":"1","actor":"admin"}`, "application/json", "", 400},
		{"POST", "/reconcile", `{"limit":1} {}`, "application/json", "", 400},
	} {
		r := httptest.NewRequest(tc.method, testHTTPResource+tc.path, strings.NewReader(tc.body))
		if tc.body == "" {
			r.ContentLength = 0
			r.Body = io.NopCloser(strings.NewReader(""))
		}
		r.Header.Set("Authorization", "Bearer fixture-credential")
		if tc.kind != "" {
			r.Header.Set("Content-Type", tc.kind)
		}
		if tc.disposition != "" {
			r.Header.Set("Content-Disposition", tc.disposition)
		}
		w := &deadlineRecorder{httptest.NewRecorder()}
		endpoint.ServeHTTP(w, r)
		var failure bridge.Fault
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &failure) != nil || failure.Outcome != bridge.NotExecuted || failure.Code == "" {
			t.Fatal("fixed route/parser rejection changed", tc.path, w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET, POST" {
			t.Fatal("wrong method lost useful Allow", w.Header())
		}
	}
	if gates.Load() != 0 {
		t.Fatal("invalid HTTP request reached operation gate", gates.Load())
	}
}
