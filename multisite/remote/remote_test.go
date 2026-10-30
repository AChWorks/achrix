// SPDX-License-Identifier: MPL-2.0
package remote_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/multisite"
	"github.com/AChWorks/achrix/multisite/remote"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	httpURL = "https://product.example/api/multisite"
	mcpURL  = "https://product.example/mcp/multisite"
	forged  = "UNTRUSTED_REQUEST_ID"
)

type evidence struct {
	mu         sync.Mutex
	events     []bridge.Diagnostic
	coreCauses []error
	coreCalls  []bridge.Request
}

type fixture struct {
	service *multisite.Service
	http    *bridge.Endpoint
	mcp     *bridge.Endpoint
	proof   *evidence
}

func newFixture(t *testing.T, sites []multisite.Site, policy achrix.Policy) *fixture {
	t.Helper()
	module, err := multisite.NewManaged(multisite.Config{Sites: sites}, multisite.ManagementConfig{})
	if err != nil {
		t.Fatal(err)
	}
	proof := new(evidence)
	app, err := achrix.New(achrix.Config{
		StartupTimeout: time.Second, ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Diagnostic: func(ctx context.Context, _, _ string, cause error) {
			request, _ := bridge.RequestFromContext(ctx)
			proof.mu.Lock()
			defer proof.mu.Unlock()
			proof.coreCauses = append(proof.coreCauses, cause)
			proof.coreCalls = append(proof.coreCalls, request)
		},
	}, policy, module)
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
	service, err := multisite.NewService(app, module)
	if err != nil {
		t.Fatal(err)
	}
	config := func(channel, resource string) bridge.Config {
		return bridge.Config{
			ResourceURL: resource, AuthorizationServers: []string{"https://issuer.example"}, Scopes: []string{"multisite"},
			// Explicit finite fixture credentials establish resource-specific
			// mapping only, not a production OAuth provider or user consent.
			Verify: func(_ context.Context, token string) (bridge.Credential, error) {
				var principal achrix.Principal
				switch token {
				case channel + ".reader":
					principal = "reader"
				case channel + ".admin":
					principal = "admin"
				default:
					return bridge.Credential{}, bridge.ErrInvalidCredential
				}
				return bridge.Credential{
					Caller:    bridge.Caller{Principal: principal, ClientID: "client-" + channel, ConnectionID: "connection-" + string(principal)},
					ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"multisite"},
				}, nil
			},
			Admit: func(context.Context, bridge.Caller) error { return nil },
			Diagnostic: func(_ context.Context, event bridge.Diagnostic) {
				proof.mu.Lock()
				defer proof.mu.Unlock()
				proof.events = append(proof.events, event)
			},
		}
	}
	httpEndpoint, err := remote.NewHTTP(service, config("http", httpURL))
	if err != nil {
		t.Fatal(err)
	}
	mcpEndpoint, err := remote.NewMCP(service, config("mcp", mcpURL))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{service: service, http: httpEndpoint, mcp: mcpEndpoint, proof: proof}
}

func ordinarySites() []multisite.Site {
	return []multisite.Site{
		{ID: "a", Authorities: []string{"a.private.example"}},
		{ID: "hidden", Authorities: []string{"hidden.private.example"}},
		{ID: "disabled", Authorities: []string{"disabled.private.example"}, Disabled: true},
		{ID: "z", Authorities: []string{"z.private.example"}},
	}
}

func ordinaryPolicy(_ context.Context, actor achrix.Principal, operation, target string) error {
	if operation != multisite.InventoryRead {
		return achrix.ErrDenied
	}
	if actor == "admin" || actor == "reader" && (target == multisite.InventoryTarget || target == "a" || target == "z") {
		return nil
	}
	return achrix.ErrDenied
}

type fault struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	Reference string            `json:"reference"`
	RequestID string            `json:"request_id"`
}

type result struct {
	response *httptest.ResponseRecorder
	data     json.RawMessage
	failure  *fault
}

func sendHTTP(t *testing.T, handler http.Handler, method, target, token string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", forged)
	request.Header.Set("X-Principal", "admin")
	request.Header.Set("X-Client-ID", "forged-client")
	request.Header.Set("X-Site-ID", "hidden")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if id := response.Header().Get("X-Request-ID"); id == "" || id == forged {
		t.Fatal("request correlation was absent or caller-controlled", response.Header())
	}
	return response
}

func sendRPC(t *testing.T, handler http.Handler, token, method string, params map[string]any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	params["_meta"] = map[string]any{
		mcp.MetaKeyProtocolVersion: "2026-07-28", mcp.MetaKeyClientCapabilities: map[string]any{},
		mcp.MetaKeyClientInfo: map[string]any{"name": "multisite-contract-fixture", "version": "1"},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, mcpURL, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("Mcp-Name", name)
	}
	request.Header.Set("X-Request-ID", forged)
	request.Header.Set("X-Principal", "admin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if id := response.Header().Get("X-Request-ID"); id == "" || id == forged {
		t.Fatal("request correlation was absent or caller-controlled")
	}
	if response.Code != http.StatusOK {
		return response, nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Error) > 0 || len(envelope.Result) == 0 {
		t.Fatal("unexpected MCP envelope", response.Body.String(), err)
	}
	return response, envelope.Result
}

func read(t *testing.T, f *fixture, channel, user, operation string, arguments map[string]any) result {
	t.Helper()
	var out result
	if channel == "http" {
		target := httpURL + "/sites"
		if operation == remote.SiteOperation {
			target += "/" + url.PathEscape(arguments["site_id"].(string))
		} else {
			query := make(url.Values)
			for key, value := range arguments {
				query.Set(key, fmt.Sprint(value))
			}
			if len(query) > 0 {
				target += "?" + query.Encode()
			}
		}
		out.response = sendHTTP(t, f.http, http.MethodGet, target, "http."+user, nil)
		out.data = append(json.RawMessage(nil), out.response.Body.Bytes()...)
	} else {
		var raw json.RawMessage
		out.response, raw = sendRPC(t, f.mcp, "mcp."+user, "tools/call", map[string]any{"name": operation, "arguments": arguments})
		if out.response.Code == http.StatusOK {
			var tool struct {
				IsError           bool                    `json:"isError"`
				StructuredContent json.RawMessage         `json:"structuredContent"`
				Content           []struct{ Text string } `json:"content"`
			}
			if err := json.Unmarshal(raw, &tool); err != nil {
				t.Fatal(err)
			}
			out.data = tool.StructuredContent
			if tool.IsError {
				if len(tool.Content) != 1 {
					t.Fatal("missing bounded MCP failure", string(raw))
				}
				out.data = json.RawMessage(tool.Content[0].Text)
				out.failure = new(fault)
			}
		} else {
			out.data = out.response.Body.Bytes()
		}
	}
	if out.response.Code >= 400 {
		out.failure = new(fault)
	}
	if out.failure != nil {
		if err := json.Unmarshal(out.data, out.failure); err != nil {
			t.Fatal(err)
		}
		if out.failure.Code == "" || out.failure.Message == "" || out.failure.Action == "" || out.failure.RequestID != out.response.Header().Get("X-Request-ID") {
			t.Fatal("failure lost safe meaning/action/correlation", string(out.data))
		}
	}
	return out
}

func TestBothBindingsPreserveSparseTraversalAndCurrentPermissions(t *testing.T) {
	sites := []multisite.Site{{ID: "a", Authorities: []string{"a.private.example"}}, {ID: "z", Authorities: []string{"z.private.example"}}}
	for i := 0; i < 240; i++ {
		id := fmt.Sprintf("hidden-%03d", i)
		sites = append(sites, multisite.Site{ID: multisite.SiteID(id), Authorities: []string{id + ".private.example"}})
	}
	var mu sync.Mutex
	allowZ, allowCollection := true, true
	policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, operation, target string) error {
		mu.Lock()
		defer mu.Unlock()
		if actor == "reader" && operation == multisite.InventoryRead && (target == "" && allowCollection || target == "a" || target == "z" && allowZ) {
			return nil
		}
		return achrix.ErrDenied
	})
	f := newFixture(t, sites, policy)
	for _, channel := range []string{"http", "mcp"} {
		t.Run(channel, func(t *testing.T) {
			mu.Lock()
			allowZ, allowCollection = true, true
			mu.Unlock()
			first := read(t, f, channel, "reader", remote.InventoryOperation, map[string]any{"limit": 1})
			var page remote.InventoryPage
			if first.failure != nil || json.Unmarshal(first.data, &page) != nil || len(page.Sites) != 1 || page.Sites[0].ID != "a" || page.NextCursor == "" {
				t.Fatal("first page lost authorized site/cursor", string(first.data))
			}
			decoded, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
			if err != nil || string(decoded) != "a" || strings.Contains(string(first.data), "hidden") || strings.Contains(string(first.data), "private.example") {
				t.Fatal("cursor or page disclosed a denied identity or authority", string(first.data))
			}
			cursor := page.NextCursor
			second := read(t, f, channel, "reader", remote.InventoryOperation, map[string]any{"limit": 1, "cursor": cursor})
			page = remote.InventoryPage{}
			if second.failure != nil || json.Unmarshal(second.data, &page) != nil || len(page.Sites) != 1 || page.Sites[0].ID != "z" || page.NextCursor != "" {
				t.Fatal("sparse authorized site was unreachable", string(second.data))
			}
			mu.Lock()
			allowZ = false
			mu.Unlock()
			revoked := read(t, f, channel, "reader", remote.InventoryOperation, map[string]any{"limit": 1, "cursor": cursor})
			page = remote.InventoryPage{}
			if revoked.failure != nil || json.Unmarshal(revoked.data, &page) != nil || len(page.Sites) != 0 || !strings.Contains(string(revoked.data), `"sites":[]`) {
				t.Fatal("cursor retained revoked site access or nil collection", string(revoked.data))
			}
			mu.Lock()
			allowCollection = false
			mu.Unlock()
			denied := read(t, f, channel, "reader", remote.InventoryOperation, map[string]any{"limit": 1, "cursor": cursor})
			if denied.failure == nil || denied.failure.Code != "denied" {
				t.Fatal("cursor bypassed current collection authorization", string(denied.data))
			}
		})
	}
}

func TestAbsentLimitSelectsTwentyAcrossBothChannels(t *testing.T) {
	sites := make([]multisite.Site, 25)
	for i := range sites {
		id := fmt.Sprintf("site-%02d", i)
		sites[i] = multisite.Site{ID: multisite.SiteID(id), Authorities: []string{id + ".private.example"}}
	}
	f := newFixture(t, sites, achrix.PolicyFunc(ordinaryPolicy))
	for _, channel := range []string{"http", "mcp"} {
		actual := read(t, f, channel, "admin", remote.InventoryOperation, map[string]any{})
		var page remote.InventoryPage
		if actual.failure != nil || json.Unmarshal(actual.data, &page) != nil || len(page.Sites) != remote.DefaultLimit || remote.DefaultLimit != 20 || page.NextCursor == "" {
			t.Fatal("absent limit changed its published default", channel, string(actual.data))
		}
	}
}

func TestExactTargetReadAndSafeInputContracts(t *testing.T) {
	f := newFixture(t, ordinarySites(), achrix.PolicyFunc(ordinaryPolicy))
	for _, channel := range []string{"http", "mcp"} {
		t.Run(channel, func(t *testing.T) {
			for _, id := range []string{"hidden", "missing"} {
				denied := read(t, f, channel, "reader", remote.SiteOperation, map[string]any{"site_id": id})
				if denied.failure == nil || denied.failure.Code != "denied" || denied.failure.Reference != "" || channel == "http" && denied.response.Code != http.StatusForbidden {
					t.Fatal("exact-site denial leaked existence/authority", string(denied.data))
				}
			}
			missing := read(t, f, channel, "admin", remote.SiteOperation, map[string]any{"site_id": "missing"})
			if missing.failure == nil || missing.failure.Code != "site_not_found" || channel == "http" && missing.response.Code != http.StatusNotFound {
				t.Fatal("authorized missing site mapping", string(missing.data))
			}
			disabled := read(t, f, channel, "admin", remote.SiteOperation, map[string]any{"site_id": "disabled"})
			var site remote.SiteInfo
			if disabled.failure != nil || json.Unmarshal(disabled.data, &site) != nil || site.ID != "disabled" || !site.Disabled || !strings.Contains(string(disabled.data), `"modules":[]`) {
				t.Fatal("disabled metadata or empty module representation changed", string(disabled.data))
			}
			direct, err := f.service.Site(context.Background(), "admin", "disabled")
			if err != nil || site.Revision != direct.Revision {
				t.Fatal("transport changed opaque revision", site, direct, err)
			}
			for _, args := range []map[string]any{{"limit": 0}, {"limit": 101}, {"cursor": "PRIVATE_INVALID_CURSOR!"}, {"actor": "admin"}} {
				bad := read(t, f, channel, "reader", remote.InventoryOperation, args)
				if bad.failure == nil || bad.failure.Code != "invalid_arguments" || len(bad.failure.Fields) == 0 || strings.Contains(string(bad.data), "PRIVATE_INVALID_CURSOR") {
					t.Fatal("invalid input lacks safe field guidance", args, string(bad.data))
				}
			}
			for _, args := range []map[string]any{{"limit": 1}, {"limit": 100}, {}} {
				good := read(t, f, channel, "reader", remote.InventoryOperation, args)
				if good.failure != nil {
					t.Fatal("valid boundary/default rejected", args, string(good.data))
				}
			}
		})
	}
}

func TestHTTPRejectsAmbiguousAndUnboundedReadRequests(t *testing.T) {
	f := newFixture(t, ordinarySites(), achrix.PolicyFunc(ordinaryPolicy))
	for _, tc := range []struct {
		name, method, suffix, body string
		status                     int
	}{
		{"duplicate limit", "GET", "/sites?limit=1&limit=2", "", 400},
		{"duplicate cursor", "GET", "/sites?cursor=&cursor=YQ", "", 400},
		{"unknown actor", "GET", "/sites?actor=admin", "", 400},
		{"invalid query", "GET", "/sites?cursor=%GG", "", 400},
		{"oversized query", "GET", "/sites?cursor=" + strings.Repeat("a", 513), "", 400},
		{"site query", "GET", "/sites/a?limit=1", "", 400},
		{"invalid number", "GET", "/sites?limit=one", "", 400},
		{"GET body", "GET", "/sites", `{"actor":"admin"}`, 400},
		{"wrong method", "POST", "/sites", "", 405},
		{"unknown route", "GET", "/resolve", "", 404},
		{"trailing collection slash", "GET", "/sites/", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := sendHTTP(t, f.http, tc.method, httpURL+tc.suffix, "http.reader", strings.NewReader(tc.body))
			var failure fault
			if response.Code != tc.status || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Code == "" || failure.Outcome != bridge.NotExecuted {
				t.Fatal("request rejection lost bounded meaning", response.Code, response.Body.String())
			}
			if response.Code == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodGet {
				t.Fatal("method guidance advertised the wrong protocol", response.Header())
			}
		})
	}
	for _, suffix := range []string{"/sites/../sites/a", "/sites//a", "/sites/%61", "/sites/a%2Fhidden"} {
		response := sendHTTP(t, f.http, http.MethodGet, httpURL+suffix, "http.reader", nil)
		if response.Code < 400 || response.Code >= 500 || response.Header().Get("Location") != "" {
			t.Fatal("noncanonical path was accepted or redirected", suffix, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, httpURL+"/sites", strings.NewReader("x"))
	request.ContentLength = -1
	request.Header.Set("Authorization", "Bearer http.reader")
	response := httptest.NewRecorder()
	f.http.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatal("unknown-length GET body was accepted", response.Code, response.Body.String())
	}
	wrongAudience := sendHTTP(t, f.http, http.MethodGet, httpURL+"/sites", "mcp.reader", nil)
	if wrongAudience.Code != http.StatusUnauthorized {
		t.Fatal("MCP credential accepted at HTTP audience", wrongAudience.Code)
	}
	wrongAudience, _ = sendRPC(t, f.mcp, "http.reader", "tools/list", map[string]any{})
	if wrongAudience.Code != http.StatusUnauthorized {
		t.Fatal("HTTP credential accepted at MCP audience", wrongAudience.Code)
	}
}

func TestPolicyFailureAbortsPageAndPreservesProtectedAttribution(t *testing.T) {
	private := errors.New("PRIVATE_POLICY_BACKEND_CAUSE")
	f := newFixture(t, ordinarySites(), achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, operation, target string) error {
		if target == "hidden" {
			return private
		}
		return ordinaryPolicy(context.Background(), actor, operation, target)
	}))
	for _, channel := range []string{"http", "mcp"} {
		failed := read(t, f, channel, "reader", remote.InventoryOperation, map[string]any{})
		if failed.failure == nil || failed.failure.Code != "authorization_unavailable" || failed.failure.Reference != "" || channel == "http" && failed.response.Code != http.StatusServiceUnavailable {
			t.Fatal("policy failure was mapped as denial or partial success", string(failed.data))
		}
		if strings.Contains(string(failed.data), "PRIVATE_POLICY") || strings.Contains(string(failed.data), `"sites"`) {
			t.Fatal("partial inventory or protected cause escaped", string(failed.data))
		}
		requestID := failed.response.Header().Get("X-Request-ID")
		f.proof.mu.Lock()
		originalFound, bridgeFound := false, false
		for i, cause := range f.proof.coreCauses {
			request := f.proof.coreCalls[i]
			if errors.Is(cause, private) && request.ID == requestID && request.Channel == channel && request.Caller.Principal == "reader" {
				originalFound = true
			}
		}
		for _, event := range f.proof.events {
			if event.Request.ID == requestID && event.Request.Channel == channel && event.Request.Operation == remote.InventoryOperation && event.Request.Caller.Principal == "reader" && event.Request.Caller.ClientID == "client-"+channel && event.Code == "authorization_unavailable" && errors.Is(event.Cause, achrix.ErrAuthorizationUnavailable) {
				bridgeFound = true
			}
		}
		f.proof.mu.Unlock()
		if !originalFound || !bridgeFound {
			t.Fatal("protected cause or trusted transport attribution was lost", channel, originalFound, bridgeFound)
		}
	}
}

func TestPublishedSchemasMatchActualToolsAndResponses(t *testing.T) {
	f := newFixture(t, ordinarySites(), achrix.PolicyFunc(ordinaryPolicy))
	var document struct {
		OpenAPI    string                                       `json:"openapi"`
		Components struct{ Schemas map[string]json.RawMessage } `json:"components"`
		Paths      map[string]struct {
			Get struct {
				OperationID string `json:"operationId"`
				Parameters  []struct {
					Name   string
					Schema json.RawMessage
				}
			} `json:"get"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(remote.OpenAPI(), &document); err != nil || document.OpenAPI != "3.1.1" || len(document.Paths) != 2 {
		t.Fatal("invalid OpenAPI surface", err)
	}
	copy := remote.OpenAPI()
	copy[0] = '!'
	if remote.OpenAPI()[0] != '{' {
		t.Fatal("caller mutated the published schema")
	}
	response, raw := sendRPC(t, f.mcp, "mcp.reader", "tools/list", map[string]any{})
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
	if response.Code != http.StatusOK || json.Unmarshal(raw, &catalog) != nil || len(catalog.Tools) != 2 {
		t.Fatal("unexpected bounded catalog", response.Code, string(raw))
	}
	mapping := map[string][2]string{
		remote.InventoryOperation: {"InventoryInput", "InventoryPage"},
		remote.SiteOperation:      {"SiteInput", "SiteInfo"},
	}
	for _, tool := range catalog.Tools {
		pair, known := mapping[tool.Name]
		if !known || !tool.Annotations.ReadOnly || !tool.Annotations.Idempotent || tool.Annotations.Destructive || tool.Annotations.OpenWorld {
			t.Fatal("catalog exposed an unexpected operation or effect", tool)
		}
		if !sameJSON(t, tool.InputSchema, document.Components.Schemas[pair[0]]) || !sameJSON(t, tool.OutputSchema, document.Components.Schemas[pair[1]]) {
			t.Fatal("MCP input/output drifted from published schemas", tool.Name, string(tool.InputSchema), string(tool.OutputSchema))
		}
	}
	if document.Paths["/sites"].Get.OperationID != remote.InventoryOperation || document.Paths["/sites/{site_id}"].Get.OperationID != remote.SiteOperation {
		t.Fatal("HTTP operation identity drifted")
	}
	for _, test := range []struct {
		operation, schema string
		arguments         map[string]any
	}{
		{remote.InventoryOperation, "InventoryPage", map[string]any{}},
		{remote.SiteOperation, "SiteInfo", map[string]any{"site_id": "a"}},
	} {
		for _, channel := range []string{"http", "mcp"} {
			actual := read(t, f, channel, "reader", test.operation, test.arguments)
			if actual.failure != nil {
				t.Fatal("schema proof did not execute a successful read", string(actual.data))
			}
			validateJSON(t, document.Components.Schemas[test.schema], actual.data)
		}
	}
	failed := read(t, f, "http", "reader", remote.SiteOperation, map[string]any{"site_id": "hidden"})
	validateJSON(t, document.Components.Schemas["Problem"], failed.data)
	// Path/query schemas are the same fragments used by the respective tool.
	for route, inputName := range map[string]string{"/sites": "InventoryInput", "/sites/{site_id}": "SiteInput"} {
		var input struct{ Properties map[string]json.RawMessage }
		if err := json.Unmarshal(document.Components.Schemas[inputName], &input); err != nil {
			t.Fatal(err)
		}
		for _, parameter := range document.Paths[route].Get.Parameters {
			if !sameJSON(t, parameter.Schema, input.Properties[parameter.Name]) {
				t.Fatal("HTTP parameter drifted from shared typed input", route, parameter.Name)
			}
		}
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

func validateJSON(t *testing.T, schema, instance []byte) {
	t.Helper()
	var definition jsonschema.Schema
	if err := json.Unmarshal(schema, &definition); err != nil {
		t.Fatal(err)
	}
	resolved, err := definition.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(instance, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatal("response violates published schema", err, string(instance))
	}
}
