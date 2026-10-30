// SPDX-License-Identifier: MPL-2.0

package bridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These are disposable verifier inputs, not a production token issuer. Product
// permission and PostgreSQL acknowledgement-loss proofs live in Notes.
const contractToken = "contract-disposable-valid-token"

var contractVersions = []string{"2025-11-25", "2026-07-28"}

type contractInput struct {
	Text string `json:"text"`
}

type contractOutput struct {
	Text         string `json:"text"`
	Principal    string `json:"principal"`
	ClientID     string `json:"client_id"`
	ConnectionID string `json:"connection_id"`
	RequestID    string `json:"request_id"`
}

type contractFailure struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Detail    string            `json:"detail"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	Reference string            `json:"reference"`
	RequestID string            `json:"request_id"`
}

type contractRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		Tools             []struct {
			Name string `json:"name"`
		} `json:"tools"`
		ProtocolVersion   string   `json:"protocolVersion"`
		SupportedVersions []string `json:"supportedVersions"`
		Capabilities      *struct {
			Tools *struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
	} `json:"result"`
	Error *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

type contractHTTPResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

type contractDiagnostic struct {
	Event   bridge.Diagnostic
	Context bridge.Request
	Present bool
}

type contractCapture struct {
	mu     sync.Mutex
	events []contractDiagnostic
}

func (c *contractCapture) record(ctx context.Context, event bridge.Diagnostic) {
	r, ok := bridge.RequestFromContext(ctx)
	c.mu.Lock()
	c.events = append(c.events, contractDiagnostic{event, r, ok})
	c.mu.Unlock()
}

func (c *contractCapture) snapshot() []contractDiagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]contractDiagnostic(nil), c.events...)
}

type contractFixture struct {
	endpoint *bridge.Endpoint
	server   *httptest.Server
	client   *http.Client
	capture  *contractCapture
}

func contractCredential() bridge.Credential {
	return bridge.Credential{
		Caller: bridge.Caller{
			Principal: "product-user-one", ClientID: "registered-client", ConnectionID: "connection-one",
		},
		ExpiresAt: time.Now().Add(time.Hour),
		Scopes:    []string{"notes"},
	}
}

func newContractFixture(t *testing.T, configure func(*bridge.Config), register func(*bridge.Registrar) error) *contractFixture {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	capture := &contractCapture{}
	config := bridge.Config{
		ResourceURL:          server.URL + "/mcp",
		AuthorizationServers: []string{"https://issuer.example.invalid"},
		Scopes:               []string{"notes"},
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			if token != contractToken {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return contractCredential(), nil
		},
		Admit:      func(context.Context, bridge.Caller) error { return nil },
		Diagnostic: capture.record,
		Limits: bridge.Limits{
			RequestBytes: 16 << 10, ResponseBytes: 64 << 10, Concurrent: 4, Timeout: 2 * time.Second,
		},
	}
	if configure != nil {
		configure(&config)
	}
	endpoint, err := bridge.New(config, register)
	if err != nil {
		t.Fatal("construct public Bridge consumer:", err)
	}
	mux.Handle("/mcp", endpoint)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", endpoint.MetadataHandler())
	client := server.Client()
	client.Timeout = 4 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return &contractFixture{endpoint, server, client, capture}
}

func contractTool(name string) *mcp.Tool {
	return &mcp.Tool{
		Name: name, Description: "Exercise the bounded public application contract",
		InputSchema: map[string]any{
			"type": "object", "required": []string{"text"}, "additionalProperties": false,
			"properties": map[string]any{"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}},
		},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

func contractEcho(calls *atomic.Int32) func(context.Context, contractInput) (contractOutput, error) {
	return func(ctx context.Context, input contractInput) (contractOutput, error) {
		calls.Add(1)
		r, ok := bridge.RequestFromContext(ctx)
		if !ok {
			return contractOutput{}, errors.New("authenticated request context missing")
		}
		return contractOutput{input.Text, string(r.Caller.Principal), r.Caller.ClientID, r.Caller.ConnectionID, r.ID}, nil
	}
}

func contractBody(version, method, name string, arguments any) []byte {
	params := map[string]any{}
	if method == "tools/call" {
		params["name"], params["arguments"] = name, arguments
	}
	if method == "initialize" {
		params["protocolVersion"] = version
		params["capabilities"] = map[string]any{}
		params["clientInfo"] = map[string]any{"name": "contract-client", "version": "1"}
	}
	if version == "2026-07-28" {
		params["_meta"] = map[string]any{
			mcp.MetaKeyProtocolVersion:    version,
			mcp.MetaKeyClientCapabilities: map[string]any{},
			mcp.MetaKeyClientInfo:         map[string]any{"name": "claimed-administrator-client", "version": "1"},
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "untrusted-rpc-id", "method": method, "params": params})
	if err != nil {
		panic(err) // All callers supply JSON-representable test inputs.
	}
	return body
}

func (f *contractFixture) request(version, method, name, token string, arguments any) *http.Request {
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/mcp", bytes.NewReader(contractBody(version, method, name, arguments)))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", version)
	if version == "2026-07-28" {
		req.Header.Set("Mcp-Method", method)
		if name != "" {
			req.Header.Set("Mcp-Name", name)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func (f *contractFixture) roundTrip(req *http.Request) (contractHTTPResponse, error) {
	resp, err := f.client.Do(req)
	if err != nil {
		return contractHTTPResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return contractHTTPResponse{resp.StatusCode, resp.Header.Clone(), body}, err
}

func (f *contractFixture) send(t *testing.T, req *http.Request) contractHTTPResponse {
	t.Helper()
	response, err := f.roundTrip(req)
	if err != nil {
		t.Fatal("HTTP MCP exchange:", err)
	}
	if response.Header.Get("X-Request-ID") == "" {
		t.Fatal("response omitted trusted request correlation")
	}
	if response.Header.Get("Mcp-Session-Id") != "" {
		t.Fatal("request-only profile created a persistent session identity")
	}
	return response
}

func contractRPC(t *testing.T, response contractHTTPResponse) contractRPCResponse {
	t.Helper()
	if response.Status != http.StatusOK {
		t.Fatalf("MCP response status %d: %s", response.Status, response.Body)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("request-only profile returned %q", response.Header.Get("Content-Type"))
	}
	var wire contractRPCResponse
	if err := json.Unmarshal(response.Body, &wire); err != nil || wire.JSONRPC != "2.0" {
		t.Fatalf("invalid MCP response: %s (%v)", response.Body, err)
	}
	return wire
}

func contractToolFailure(t *testing.T, response contractHTTPResponse) contractFailure {
	t.Helper()
	wire := contractRPC(t, response)
	if wire.Error != nil || !wire.Result.IsError || len(wire.Result.Content) == 0 || wire.Result.Content[0].Type != "text" {
		t.Fatalf("expected structured actionable tool failure: %s", response.Body)
	}
	if len(wire.Result.StructuredContent) != 0 {
		t.Fatal("error response violated the typed success output contract")
	}
	var failure contractFailure
	if err := json.Unmarshal([]byte(wire.Result.Content[0].Text), &failure); err != nil {
		t.Fatal("tool error lacked machine-readable details:", err)
	}
	contractUsefulFailure(t, failure, response)
	return failure
}

func contractHTTPFailure(t *testing.T, response contractHTTPResponse, status int) contractFailure {
	t.Helper()
	if response.Status != status || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("expected HTTP problem %d, got %d %q: %s", status, response.Status, response.Header.Get("Content-Type"), response.Body)
	}
	var failure contractFailure
	if err := json.Unmarshal(response.Body, &failure); err != nil {
		t.Fatal("HTTP problem lacked machine-readable details:", err)
	}
	contractUsefulFailure(t, failure, response)
	return failure
}

func contractUsefulFailure(t *testing.T, failure contractFailure, response contractHTTPResponse) {
	t.Helper()
	if failure.Code == "" || failure.Action == "" || (failure.Message == "" && failure.Detail == "") || failure.RequestID == "" {
		t.Fatalf("failure lost useful code, explanation, next action or correlation: %+v", failure)
	}
	if failure.RequestID != response.Header.Get("X-Request-ID") {
		t.Fatal("public failure correlation differs from the trusted response identity")
	}
}

func contractNoPrivate(t *testing.T, response contractHTTPResponse, private ...string) {
	t.Helper()
	wire := string(response.Body) + fmt.Sprint(response.Header)
	for _, value := range append(private, contractToken) {
		if value != "" && strings.Contains(wire, value) {
			t.Fatalf("public response exposed protected test value %q", value)
		}
	}
}

func contractCause(t *testing.T, f *contractFixture, requestID string, cause error, dispatched bool, outcome bridge.Outcome, reference string) bridge.Diagnostic {
	t.Helper()
	for _, captured := range f.capture.snapshot() {
		event := captured.Event
		if event.Request.ID != requestID || !errors.Is(event.Cause, cause) {
			continue
		}
		if event.Phase == "" || event.Dispatched != dispatched || event.Outcome != outcome || event.Reference != reference {
			t.Fatalf("protected cause lost phase/dispatch/effect/reference: %+v", event)
		}
		if !captured.Present || captured.Context.ID != requestID || captured.Context.Channel != "mcp" {
			t.Fatal("protected callback did not retain the original trusted request context")
		}
		return event
	}
	t.Fatalf("original protected cause not retained for request %s: %v", requestID, cause)
	return bridge.Diagnostic{}
}

func TestContractHTTPProtocolErasAndTrustedIdentity(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	seenIDs := map[string]bool{}
	for _, version := range contractVersions {
		t.Run(version, func(t *testing.T) {
			method := "initialize"
			if version == "2026-07-28" {
				method = "server/discover"
			}
			discovery := contractRPC(t, f.send(t, f.request(version, method, "", contractToken, nil)))
			if discovery.Error != nil || discovery.Result.Capabilities == nil || discovery.Result.Capabilities.Tools == nil || discovery.Result.Capabilities.Tools.ListChanged {
				t.Fatal("fixed catalog discovery is unavailable or advertises an idle change subscription")
			}
			if version == "2025-11-25" && discovery.Result.ProtocolVersion != version {
				t.Fatalf("legacy initialization selected an unexpected version %q", discovery.Result.ProtocolVersion)
			}
			list := contractRPC(t, f.send(t, f.request(version, "tools/list", "", contractToken, nil)))
			if list.Error != nil || len(list.Result.Tools) != 1 || list.Result.Tools[0].Name != "contract.echo" {
				t.Fatalf("supported catalog unavailable: %+v", list)
			}
			req := f.request(version, "tools/call", "contract.echo", contractToken, map[string]any{"text": "permitted output"})
			req.Header.Set("X-Request-ID", "forged-request")
			req.Header.Set("X-User-ID", "administrator")
			req.Header.Set("X-Client-ID", "claimed-client")
			response := f.send(t, req)
			wire := contractRPC(t, response)
			if wire.Error != nil || wire.Result.IsError {
				t.Fatalf("authorized call failed: %s", response.Body)
			}
			var out contractOutput
			if err := json.Unmarshal(wire.Result.StructuredContent, &out); err != nil {
				t.Fatal("typed success output unavailable:", err)
			}
			if out.Text != "permitted output" || out.Principal != "product-user-one" || out.ClientID != "registered-client" || out.ConnectionID != "connection-one" {
				t.Fatalf("untrusted metadata replaced verified identity or permitted output: %+v", out)
			}
			if out.RequestID != response.Header.Get("X-Request-ID") || out.RequestID == "forged-request" || out.RequestID == "untrusted-rpc-id" || seenIDs[out.RequestID] {
				t.Fatal("correlation is missing, caller-selected or reused")
			}
			seenIDs[out.RequestID] = true
			contractNoPrivate(t, response)
			terminals := 0
			for _, captured := range f.capture.snapshot() {
				if event := captured.Event; event.Request.ID == out.RequestID {
					terminals++
					if event.Cause != nil || !event.Dispatched || event.Outcome != bridge.Succeeded || event.Request.Operation != "contract.echo" || event.Request.Caller.Principal != "product-user-one" {
						t.Fatalf("successful call lost accountable terminal evidence: %+v", event)
					}
				}
			}
			if terminals != 1 {
				t.Fatalf("successful call emitted %d terminal events, want one", terminals)
			}
		})
	}
	if calls.Load() != int32(len(contractVersions)) {
		t.Fatal("catalog or protocol handling invoked the application unexpectedly")
	}
}

type contractSDKTransport struct {
	base        http.RoundTripper
	getRequests atomic.Int32
}

func (t *contractSDKTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		t.getRequests.Add(1)
	}
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+contractToken)
	return t.base.RoundTrip(copy)
}

func TestContractOfficialSDKDiscoveryAndInvocation(t *testing.T) {
	for _, version := range contractVersions {
		t.Run(version, func(t *testing.T) {
			var calls atomic.Int32
			f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
				return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
			})
			transport := &contractSDKTransport{base: f.client.Transport}
			httpClient := *f.client
			httpClient.Transport = transport
			client := mcp.NewClient(&mcp.Implementation{Name: "official-contract-client", Version: "1"}, &mcp.ClientOptions{
				Capabilities:   &mcp.ClientCapabilities{},
				MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
				Endpoint: f.server.URL + "/mcp", HTTPClient: &httpClient,
				DisableStandaloneSSE: true, MaxRetries: -1,
			}, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal("official SDK connection/discovery failed:", err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error("close official SDK client:", err)
				}
			})
			if initial := session.InitializeResult(); initial == nil || initial.ProtocolVersion != version {
				t.Fatal("SDK silently fell back from the requested supported protocol")
			}
			catalog, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil || len(catalog.Tools) != 1 || catalog.Tools[0].Name != "contract.echo" {
				t.Fatalf("official SDK catalog differs from the fixed operation contract: %v", err)
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "contract.echo", Arguments: contractInput{Text: "official client output"}})
			if err != nil || result.IsError {
				t.Fatalf("official SDK invocation failed: %v", err)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal("decode official SDK structured result:", err)
			}
			var out contractOutput
			if err := json.Unmarshal(data, &out); err != nil || out.Text != "official client output" || out.Principal != "product-user-one" || out.RequestID == "" {
				t.Fatalf("official SDK lost typed result or trusted user: %+v (%v)", out, err)
			}
			if calls.Load() != 1 || transport.getRequests.Load() != 0 {
				t.Fatal("SDK retried the operation or opened an idle SSE connection")
			}
		})
	}
}

func TestContractCredentialAndCurrentAdmissionFailures(t *testing.T) {
	var calls atomic.Int32
	var enabled atomic.Bool
	enabled.Store(true)
	var restricted atomic.Bool
	var revoked atomic.Bool
	private := errors.New("PRIVATE_VERIFIER_PROVIDER_DETAIL")
	f := newContractFixture(t, func(c *bridge.Config) {
		c.Verify = func(_ context.Context, token string) (bridge.Credential, error) {
			credential := contractCredential()
			switch token {
			case contractToken:
				if revoked.Load() {
					return bridge.Credential{}, bridge.ErrInvalidCredential
				}
				return credential, nil
			case "expired-test-credential":
				credential.ExpiresAt = time.Now().Add(-time.Minute)
				return credential, nil
			case "wrong-scope-test-credential":
				credential.Scopes = []string{"unrelated"}
				return credential, nil
			case "provider-unavailable":
				return bridge.Credential{}, private
			case "revoked-test-credential", "wrong-audience-test-credential":
				// The product verifier owns revocation and intended-resource checks.
				return bridge.Credential{}, bridge.ErrInvalidCredential
			default:
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
		}
		c.Admit = func(context.Context, bridge.Caller) error {
			if !enabled.Load() {
				return achrix.ErrDenied
			}
			return nil
		}
		c.Gate = func(_ context.Context, _ bridge.Caller, operation string) error {
			if restricted.Load() && operation == "contract.echo" {
				return achrix.ErrDenied
			}
			return nil
		}
	}, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	for _, version := range contractVersions {
		for _, test := range []struct {
			name, token string
			status      int
		}{
			{"missing", "", http.StatusUnauthorized},
			{"unknown", "unknown-test-credential", http.StatusUnauthorized},
			{"expired", "expired-test-credential", http.StatusUnauthorized},
			{"revoked", "revoked-test-credential", http.StatusUnauthorized},
			{"wrong-audience", "wrong-audience-test-credential", http.StatusUnauthorized},
			{"wrong-scope", "wrong-scope-test-credential", http.StatusForbidden},
			{"provider-unavailable", "provider-unavailable", http.StatusServiceUnavailable},
		} {
			t.Run(version+"/"+test.name, func(t *testing.T) {
				response := f.send(t, f.request(version, "tools/call", "contract.echo", test.token, map[string]any{"text": "not invoked"}))
				failure := contractHTTPFailure(t, response, test.status)
				if failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
					t.Fatal("credential rejection reached the application or lost non-execution evidence")
				}
				contractNoPrivate(t, response, private.Error(), test.token)
				if test.status == http.StatusUnauthorized && !strings.Contains(response.Header.Get("WWW-Authenticate"), "resource_metadata=") {
					t.Fatal("authentication challenge omitted the configured resource metadata locator")
				}
				if test.name == "provider-unavailable" {
					contractCause(t, f, failure.RequestID, private, false, bridge.NotExecuted, "")
				}
			})
		}
	}
	// Discover first, then change live admission without rebuilding or reconnecting.
	contractRPC(t, f.send(t, f.request("2026-07-28", "tools/list", "", contractToken, nil)))
	enabled.Store(false)
	for _, method := range []string{"tools/list", "tools/call"} {
		response := f.send(t, f.request("2026-07-28", method, "contract.echo", contractToken, map[string]any{"text": "not invoked"}))
		if failure := contractHTTPFailure(t, response, http.StatusForbidden); failure.Outcome != bridge.NotExecuted {
			t.Fatal("disabled connection has an untruthful execution outcome")
		}
	}
	enabled.Store(true)
	restricted.Store(true)
	response := f.send(t, f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "not invoked"}))
	if failure := contractToolFailure(t, response); failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
		t.Fatal("known tool name bypassed the current channel restriction")
	}
	restricted.Store(false)
	wire := contractRPC(t, f.send(t, f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "now permitted"})))
	if wire.Result.IsError || wire.Error != nil || calls.Load() != 1 {
		t.Fatal("restored current admission did not recover the same connection")
	}
	revoked.Store(true)
	response = f.send(t, f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "now revoked"}))
	if failure := contractHTTPFailure(t, response, http.StatusUnauthorized); failure.Outcome != bridge.NotExecuted || calls.Load() != 1 {
		t.Fatal("previously successful credential stayed authorized after revocation")
	}
}

func TestContractIngressTrustCannotBeManufacturedWithHeaders(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	for _, test := range []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"foreign-origin", http.StatusForbidden, func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.example.invalid") }},
		{"cross-site-without-origin", http.StatusForbidden, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"forged-forwarded-host", http.StatusForbidden, func(r *http.Request) {
			r.Header.Set("X-Forwarded-Host", r.URL.Host)
			r.Host = "untrusted.example.invalid"
		}},
		{"duplicate-authorization", http.StatusBadRequest, func(r *http.Request) { r.Header.Add("Authorization", "Bearer another-credential") }},
		{"unexpected-session", http.StatusBadRequest, func(r *http.Request) { r.Header.Set("Mcp-Session-Id", "invented-session") }},
		{"browser-simple-content-type", http.StatusUnsupportedMediaType, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "not invoked"})
			test.change(req)
			response := f.send(t, req)
			if failure := contractHTTPFailure(t, response, test.status); failure.Outcome != bridge.NotExecuted {
				t.Fatal("ingress rejection lost proof of non-execution")
			}
			contractNoPrivate(t, response)
		})
	}
	if calls.Load() != 0 {
		t.Fatal("untrusted ingress manufactured application authority")
	}
}

func TestContractInvalidArgumentsAndModernHeaderMismatchDoNotDispatch(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	for _, version := range contractVersions {
		for _, test := range []struct {
			name string
			args any
		}{
			{"missing-field", map[string]any{}},
			{"wrong-type", map[string]any{"text": []string{"PRIVATE_INPUT_VALUE"}}},
			{"unknown-authority-field", map[string]any{"text": "valid", "actor": "PRIVATE_INPUT_VALUE"}},
		} {
			t.Run(version+"/"+test.name, func(t *testing.T) {
				response := f.send(t, f.request(version, "tools/call", "contract.echo", contractToken, test.args))
				failure := contractToolFailure(t, response)
				if failure.Code != "invalid_arguments" || failure.Outcome != bridge.NotExecuted || len(failure.Fields) == 0 {
					t.Fatalf("schema rejection lost useful field/correction/non-execution details: %+v", failure)
				}
				contractNoPrivate(t, response, "PRIVATE_INPUT_VALUE")
			})
		}
	}
	for _, test := range []struct{ header, value string }{
		{"Mcp-Method", "tools/list"}, {"Mcp-Name", "different-tool"}, {"Mcp-Protocol-Version", "2025-11-25"},
	} {
		req := f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "not invoked"})
		req.Header.Set(test.header, test.value)
		response := f.send(t, req)
		if response.Status < 400 {
			var wire contractRPCResponse
			if err := json.Unmarshal(response.Body, &wire); err != nil || wire.Error == nil {
				t.Fatalf("modern header/body disagreement was accepted: %s", response.Body)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments or conflicting protocol routing metadata invoked the application")
	}
}

func TestContractUsefulDomainFailuresPreserveProtectedEvidence(t *testing.T) {
	for _, test := range []struct {
		outcome                    bridge.Outcome
		code, message, field, rule string
	}{
		{bridge.NotExecuted, "invalid_precondition", "A required precondition was not satisfied.", "text", "must satisfy the operation precondition"},
		{bridge.NotCommitted, "revision_conflict", "The attempted transaction was rolled back after a revision conflict.", "expected_revision", "must match the current resource revision"},
		{bridge.Unknown, "unknown_outcome", "The persistence acknowledgement was lost.", "reference", "reconcile this resource before attempting another mutation"},
	} {
		t.Run(string(test.outcome), func(t *testing.T) {
			private := errors.New("PRIVATE_DATABASE_CAUSE_" + string(test.outcome))
			var calls atomic.Int32
			f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
				tool := contractTool("contract.write")
				tool.Annotations.ReadOnlyHint = false
				return bridge.AddTool(r, tool, func(context.Context, contractInput) (contractOutput, error) {
					calls.Add(1)
					return contractOutput{}, &bridge.Fault{
						Code: test.code, Message: test.message,
						Action:  "Read this resource and reconcile the operation before retrying.",
						Fields:  map[string]string{test.field: test.rule},
						Outcome: test.outcome, Reference: "resource-one", Cause: private,
					}
				})
			})
			response := f.send(t, f.request("2026-07-28", "tools/call", "contract.write", contractToken, map[string]any{"text": "attempt"}))
			failure := contractToolFailure(t, response)
			if failure.Code != test.code || failure.Outcome != test.outcome || failure.Reference != "resource-one" || failure.Fields[test.field] != test.rule {
				t.Fatalf("domain failure was flattened or misclassified: %+v", failure)
			}
			contractNoPrivate(t, response, private.Error())
			event := contractCause(t, f, failure.RequestID, private, true, test.outcome, "resource-one")
			if event.Request.Caller.Principal != "product-user-one" || event.Request.Caller.ClientID != "registered-client" || event.Request.Operation != "contract.write" {
				t.Fatal("domain cause lost trusted actor/client/operation context")
			}
			if calls.Load() != 1 {
				t.Fatal("adapter automatically replayed a mutation")
			}
		})
	}
}

var errContractMarshalCause = errors.New("PRIVATE_OUTPUT_SERIALIZER_CAUSE")

type contractUnserializableOutput struct {
	Value string `json:"value"`
}

func (contractUnserializableOutput) MarshalJSON() ([]byte, error) {
	return nil, errContractMarshalCause
}

func TestContractConfirmedWriteSurvivesSDKOutputFailure(t *testing.T) {
	for _, test := range []string{"schema", "serialization", "response-bound"} {
		t.Run(test, func(t *testing.T) {
			var writes atomic.Int32
			f := newContractFixture(t, func(c *bridge.Config) { c.Limits.ResponseBytes = 4096 }, func(r *bridge.Registrar) error {
				tool := contractTool("contract.write")
				tool.Annotations.ReadOnlyHint = false
				if test == "serialization" {
					tool.OutputSchema = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
					return bridge.AddTool(r, tool, func(ctx context.Context, _ contractInput) (contractUnserializableOutput, error) {
						writes.Add(1)
						bridge.Confirm(ctx, "committed-resource")
						return contractUnserializableOutput{Value: "already committed"}, nil
					})
				}
				if test == "schema" {
					tool.OutputSchema = map[string]any{
						"type": "object", "required": []string{"text"},
						"properties": map[string]any{"text": map[string]any{"type": "string", "enum": []string{"valid result"}}},
					}
				}
				return bridge.AddTool(r, tool, func(ctx context.Context, _ contractInput) (contractOutput, error) {
					writes.Add(1)
					bridge.Confirm(ctx, "committed-resource")
					if test == "schema" {
						return contractOutput{Text: "PRIVATE_SCHEMA_OUTPUT_VALUE"}, nil
					}
					return contractOutput{Text: strings.Repeat("bounded-output-", 2048)}, nil
				})
			})
			response := f.send(t, f.request("2026-07-28", "tools/call", "contract.write", contractToken, map[string]any{"text": "write once"}))
			var failure contractFailure
			if test == "response-bound" && response.Status >= 500 {
				failure = contractHTTPFailure(t, response, response.Status)
			} else {
				failure = contractToolFailure(t, response)
			}
			if failure.Outcome != bridge.Succeeded || failure.Reference != "committed-resource" || writes.Load() != 1 {
				t.Fatalf("post-write delivery problem erased confirmed success/ref or replayed the write: %+v; writes=%d", failure, writes.Load())
			}
			contractNoPrivate(t, response, errContractMarshalCause.Error(), "PRIVATE_SCHEMA_OUTPUT_VALUE")
			found := false
			for _, captured := range f.capture.snapshot() {
				event := captured.Event
				if event.Request.ID != failure.RequestID || event.Cause == nil {
					continue
				}
				found = true
				if !event.Dispatched || event.Outcome != bridge.Succeeded || event.Reference != "committed-resource" || event.Phase == "" {
					t.Fatalf("output failure lost original effect evidence: %+v", event)
				}
				if test == "serialization" && !errors.Is(event.Cause, errContractMarshalCause) {
					t.Fatal("SDK serialization wrapper discarded the original cause")
				}
				if test == "schema" && (!strings.Contains(event.Cause.Error(), "output") || !strings.Contains(event.Cause.Error(), "text")) {
					t.Fatal("protected SDK schema failure lost the failing output field")
				}
			}
			if !found {
				t.Fatal("post-handler output failure omitted protected diagnosis")
			}
		})
	}
}

type contractFailingWriter struct {
	header http.Header
	cause  error
}

func (w *contractFailingWriter) Header() http.Header       { return w.header }
func (*contractFailingWriter) WriteHeader(int)             {}
func (w *contractFailingWriter) Write([]byte) (int, error) { return 0, w.cause }

func TestContractResponseDeliveryFailureRetainsConfirmedEffect(t *testing.T) {
	var writes atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		tool := contractTool("contract.write")
		tool.Annotations.ReadOnlyHint = false
		return bridge.AddTool(r, tool, func(ctx context.Context, _ contractInput) (contractOutput, error) {
			writes.Add(1)
			bridge.Confirm(ctx, "committed-before-delivery")
			return contractOutput{Text: "saved"}, nil
		})
	})
	private := errors.New("PRIVATE_RESPONSE_WRITER_CAUSE")
	writer := &contractFailingWriter{header: make(http.Header), cause: private}
	req := f.request("2026-07-28", "tools/call", "contract.write", contractToken, map[string]any{"text": "write once"})
	// A deterministic failing writer isolates the delivery boundary; all other
	// protocol cases above execute through the actual HTTP server and client.
	f.endpoint.ServeHTTP(writer, req)
	requestID := writer.Header().Get("X-Request-ID")
	if requestID == "" || writes.Load() != 1 {
		t.Fatal("delivery fault changed invocation or correlation")
	}
	contractCause(t, f, requestID, private, true, bridge.Succeeded, "committed-before-delivery")
}

func TestContractRequestOnlyProfileRejectsIdleStreams(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	for _, version := range contractVersions {
		req := f.request(version, "tools/list", "", contractToken, nil)
		req.Method, req.Body, req.ContentLength = http.MethodGet, nil, 0
		response := f.send(t, req)
		if response.Status != http.StatusMethodNotAllowed {
			t.Fatalf("GET admitted an idle stream: %d", response.Status)
		}
	}
	start := time.Now()
	response := f.send(t, f.request("2026-07-28", "subscriptions/listen", "", contractToken, nil))
	if time.Since(start) > time.Second || strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("unsupported subscription parked an idle request")
	}
	if response.Status < 400 {
		wire := contractRPC(t, response)
		if wire.Error == nil && !wire.Result.IsError {
			t.Fatal("request-only profile advertised successful subscription listening")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("idle protocol handling invoked a product operation")
	}
	// A denied idle request must release its resources for an ordinary call.
	wire := contractRPC(t, f.send(t, f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": "after idle rejection"})))
	if wire.Error != nil || wire.Result.IsError || calls.Load() != 1 {
		t.Fatal("idle request rejection left the endpoint unavailable")
	}
}

func TestContractBodyLimitsApplyToKnownAndChunkedRequests(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, func(c *bridge.Config) { c.Limits.RequestBytes = 1024 }, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	for _, chunked := range []bool{false, true} {
		req := f.request("2026-07-28", "tools/call", "contract.echo", contractToken, map[string]any{"text": strings.Repeat("x", 4096)})
		if chunked {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
		}
		response := f.send(t, req)
		failure := contractHTTPFailure(t, response, http.StatusRequestEntityTooLarge)
		if failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
			t.Fatal("oversized body entered application code")
		}
	}
}

func TestContractIncompleteBodyTimeoutRetainsTransportCause(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, func(c *bridge.Config) { c.Limits.Timeout = 100 * time.Millisecond }, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := f.request("2026-07-28", "tools/call", "contract.echo", contractToken, nil)
	req.Body, req.GetBody, req.ContentLength = reader, nil, -1
	req.TransferEncoding = []string{"chunked"}
	type result struct {
		response contractHTTPResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := f.roundTrip(req)
		done <- result{response, err}
	}()
	if _, err := writer.Write([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":`)); err != nil {
		t.Fatal("send incomplete chunked request:", err)
	}
	var response contractHTTPResponse
	select {
	case completed := <-done:
		if completed.err != nil {
			t.Fatal("body deadline discarded the actionable HTTP response:", completed.err)
		}
		response = completed.response
	case <-time.After(2 * time.Second):
		t.Fatal("body read outlived its configured deadline")
	}
	failure := contractHTTPFailure(t, response, http.StatusRequestTimeout)
	if failure.Outcome != bridge.NotExecuted || failure.Code == "invalid_arguments" || failure.Code == "invalid_request" || calls.Load() != 0 {
		t.Fatal("body timeout became a schema error or invoked the application")
	}
	found := false
	for _, captured := range f.capture.snapshot() {
		event := captured.Event
		if event.Request.ID != failure.RequestID {
			continue
		}
		var timeout net.Error
		if errors.As(event.Cause, &timeout) && timeout.Timeout() {
			found = true
			if event.Dispatched || event.Outcome != bridge.NotExecuted || event.Phase == "" || event.Phase == "invoke" {
				t.Fatalf("body timeout lost original phase/non-execution evidence: %+v", event)
			}
		}
	}
	if !found {
		t.Fatal("SDK body decoding erased the original typed transport timeout")
	}
}

func TestContractConcurrentAdmissionIsBoundedAndReleased(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	f := newContractFixture(t, func(c *bridge.Config) { c.Limits.Concurrent = 1 }, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.wait"), func(ctx context.Context, input contractInput) (contractOutput, error) {
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return contractOutput{}, ctx.Err()
				}
			}
			return contractOutput{Text: input.Text}, nil
		})
	})
	type result struct {
		response contractHTTPResponse
		err      error
	}
	first := make(chan result, 1)
	go func() {
		response, err := f.roundTrip(f.request("2026-07-28", "tools/call", "contract.wait", contractToken, map[string]any{"text": "first"}))
		first <- result{response, err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first application call was not admitted")
	}
	response := f.send(t, f.request("2026-07-28", "tools/call", "contract.wait", contractToken, map[string]any{"text": "second"}))
	if failure := contractHTTPFailure(t, response, http.StatusServiceUnavailable); failure.Outcome != bridge.NotExecuted || calls.Load() != 1 {
		t.Fatal("full admission queued or invoked another operation")
	}
	unblock()
	select {
	case completed := <-first:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		if wire := contractRPC(t, completed.response); wire.Error != nil || wire.Result.IsError {
			t.Fatal("admitted operation failed after its capacity was released")
		}
	case <-time.After(time.Second):
		t.Fatal("admitted request did not finish")
	}
	wire := contractRPC(t, f.send(t, f.request("2026-07-28", "tools/call", "contract.wait", contractToken, map[string]any{"text": "third"})))
	if wire.Error != nil || wire.Result.IsError || calls.Load() != 2 {
		t.Fatal("completed request leaked its admission slot")
	}
}

func TestContractInvocationDeadlineInBothProtocolEras(t *testing.T) {
	for _, version := range contractVersions {
		t.Run(version, func(t *testing.T) {
			var calls atomic.Int32
			f := newContractFixture(t, func(c *bridge.Config) { c.Limits.Timeout = 100 * time.Millisecond }, func(r *bridge.Registrar) error {
				tool := contractTool("contract.wait")
				tool.Annotations.ReadOnlyHint = false
				return bridge.AddTool(r, tool, func(ctx context.Context, _ contractInput) (contractOutput, error) {
					calls.Add(1)
					select {
					case <-ctx.Done():
						return contractOutput{}, fmt.Errorf("PRIVATE_TIMEOUT_PROVIDER: %w", ctx.Err())
					case <-time.After(1500 * time.Millisecond):
						return contractOutput{}, errors.New("application did not receive the configured deadline")
					}
				})
			})
			response := f.send(t, f.request(version, "tools/call", "contract.wait", contractToken, map[string]any{"text": "wait"}))
			var failure contractFailure
			// Cancellation can close the SDK exchange before its tool result is
			// delivered. The bounded HTTP fallback must preserve the same evidence.
			switch response.Status {
			case http.StatusOK:
				failure = contractToolFailure(t, response)
			case http.StatusServiceUnavailable:
				failure = contractHTTPFailure(t, response, http.StatusServiceUnavailable)
			default:
				t.Fatalf("unexpected invocation-timeout response %d: %s", response.Status, response.Body)
			}
			if failure.Code != "deadline_exceeded" || failure.Outcome != bridge.Unknown || calls.Load() != 1 {
				t.Fatal("post-dispatch timeout lost its meaning, became non-execution or caused automatic replay")
			}
			contractNoPrivate(t, response, "PRIVATE_TIMEOUT_PROVIDER")
			event := contractCause(t, f, failure.RequestID, context.DeadlineExceeded, true, bridge.Unknown, "")
			if !strings.Contains(event.Cause.Error(), "PRIVATE_TIMEOUT_PROVIDER") {
				t.Fatal("protected timeout evidence lost the original application's wrapped cause")
			}
		})
	}
}

func TestContractHeaderlessLegacyRejectsBatchBeforeInvocation(t *testing.T) {
	var writes atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		first := contractTool("contract.first_write")
		first.Annotations.ReadOnlyHint = false
		if err := bridge.AddTool(r, first, func(ctx context.Context, _ contractInput) (contractOutput, error) {
			writes.Add(1)
			bridge.Confirm(ctx, "first-confirmed-reference")
			return contractOutput{Text: "first confirmed"}, nil
		}); err != nil {
			return err
		}
		second := contractTool("contract.second_write")
		second.Annotations.ReadOnlyHint = false
		return bridge.AddTool(r, second, func(context.Context, contractInput) (contractOutput, error) {
			writes.Add(1)
			return contractOutput{}, &bridge.Fault{Code: "unknown_write", Message: "Acknowledgement was lost.", Action: "Reconcile the authorized reference before repeating the write.", Outcome: bridge.Unknown, Reference: "second-unknown-reference", Cause: errors.New("synthetic second write acknowledgement loss")}
		})
	})
	batch, err := json.Marshal([]map[string]any{
		{"jsonrpc": "2.0", "id": "first", "method": "tools/call", "params": map[string]any{"name": "contract.first_write", "arguments": map[string]any{"text": "first"}}},
		{"jsonrpc": "2.0", "id": "second", "method": "tools/call", "params": map[string]any{"name": "contract.second_write", "arguments": map[string]any{"text": "second"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := f.request("2025-11-25", "tools/call", "contract.first_write", contractToken, nil)
	request.Header.Del("Mcp-Protocol-Version")
	request.Body = io.NopCloser(bytes.NewReader(batch))
	request.ContentLength = int64(len(batch))
	response := f.send(t, request)
	if writes.Load() != 0 {
		t.Fatalf("headerless JSON-RPC batch invoked %d operations under one request state: status=%d body=%s", writes.Load(), response.Status, response.Body)
	}
	failure := contractHTTPFailure(t, response, http.StatusBadRequest)
	if failure.Outcome != bridge.NotExecuted || failure.Reference != "" {
		t.Fatalf("rejected batch lost truthful non-execution: %+v", failure)
	}
	guidance := strings.ToLower(failure.Action)
	if !strings.Contains(guidance, "one") && !strings.Contains(guidance, "single") {
		t.Fatalf("batch rejection lacks single-request corrective guidance: %q", failure.Action)
	}
	for _, captured := range f.capture.snapshot() {
		if captured.Event.Dispatched || captured.Event.Outcome != bridge.NotExecuted || captured.Event.Reference != "" {
			t.Fatalf("batch contaminated operation evidence despite rejection: %+v", captured.Event)
		}
	}
}

func TestContractHeaderlessLegacyInitializationRemainsSupported(t *testing.T) {
	var calls atomic.Int32
	f := newContractFixture(t, nil, func(r *bridge.Registrar) error {
		return bridge.AddTool(r, contractTool("contract.echo"), contractEcho(&calls))
	})
	request := f.request("2025-11-25", "initialize", "", contractToken, nil)
	request.Header.Del("Mcp-Protocol-Version")
	wire := contractRPC(t, f.send(t, request))
	if wire.Error != nil || wire.Result.ProtocolVersion != "2025-11-25" || calls.Load() != 0 {
		t.Fatalf("headerless supported legacy initialization failed: %+v", wire)
	}
	// Endpoint normalization must not mutate a request shared with surrounding
	// trusted HTTP middleware; network transport alone would hide that mutation.
	request = f.request("2025-11-25", "initialize", "", contractToken, nil)
	request.Header.Del("Mcp-Protocol-Version")
	response := httptest.NewRecorder()
	f.endpoint.ServeHTTP(response, request)
	if response.Code != http.StatusOK || request.Header.Get("Mcp-Protocol-Version") != "" {
		t.Fatal("legacy default either failed initialization or mutated the caller's request headers")
	}
}

func TestContractEscapedRecoveryReferenceFitsResponseBound(t *testing.T) {
	// Use the smallest supported capacity and a maximum-length reference whose
	// JSON representation expands to six bytes for every original byte.
	const capacity = 4096
	for _, outcome := range []bridge.Outcome{bridge.Succeeded, bridge.Unknown} {
		t.Run(string(outcome), func(t *testing.T) {
			var writes atomic.Int32
			reference := strings.Repeat("<", 256)
			private := errors.New("synthetic-private-acknowledgement-cause")
			f := newContractFixture(t, func(c *bridge.Config) { c.Limits.ResponseBytes = capacity }, func(r *bridge.Registrar) error {
				tool := contractTool("contract.escaped_reference")
				tool.Annotations.ReadOnlyHint = false
				return bridge.AddTool(r, tool, func(ctx context.Context, _ contractInput) (contractOutput, error) {
					writes.Add(1)
					if outcome == bridge.Succeeded {
						bridge.Confirm(ctx, reference)
						return contractOutput{Text: strings.Repeat("large-success-payload-", 1024)}, nil
					}
					fields := make(map[string]string)
					for i := 0; i < 16; i++ {
						fields[fmt.Sprintf("field_%02d", i)] = strings.Repeat("&", 256)
					}
					return contractOutput{}, &bridge.Fault{Code: "unknown_write", Message: "Write acknowledgement was lost.", Action: "Read the authorized reference to reconcile before retrying.", Fields: fields, Outcome: bridge.Unknown, Reference: reference, Cause: private}
				})
			})
			response := f.send(t, f.request("2026-07-28", "tools/call", "contract.escaped_reference", contractToken, map[string]any{"text": "invoke once"}))
			failure := contractHTTPFailure(t, response, http.StatusInternalServerError)
			if len(response.Body) > capacity {
				t.Fatalf("overflow fallback exceeded response capacity: got %d bytes, limit %d", len(response.Body), capacity)
			}
			if writes.Load() != 1 || failure.Outcome != outcome || failure.Reference != reference || failure.RequestID == "" {
				t.Fatalf("response bound lost actual effect/reference or replayed: outcome=%q ref=%q writes=%d", failure.Outcome, failure.Reference, writes.Load())
			}
			if failure.Code != "response_too_large" || failure.Action == "" {
				t.Fatal("bounded fallback lost actionable machine meaning")
			}
			contractNoPrivate(t, response, private.Error())
			if outcome == bridge.Unknown {
				contractCause(t, f, failure.RequestID, private, true, outcome, reference)
			}
		})
	}
}
