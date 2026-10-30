// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"crypto/rand"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/multisite"
	"github.com/AChWorks/achrix/multisite/remote"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type remoteConsumerCredential struct {
	principal achrix.Principal
	audience  string
}

type remoteConsumerPolicyCall struct {
	principal achrix.Principal
	target    string
	request   bridge.Request
}

type remoteConsumerCause struct {
	request   bridge.Request
	operation string
	phase     string
	cause     error
}

// This fixture's one product policy is used by the real Service for direct,
// HTTP and MCP calls. Admission and channel gates only narrow that policy.
// Opaque fixture credentials demonstrate the resource-server seam, not an
// OAuth issuer, consent flow or a real hosted AI client's interoperability.
type remoteConsumer struct {
	service *multisite.Service
	server  *httptest.Server
	client  *http.Client
	urls    map[string]string
	tokens  map[string]string
	mu      sync.Mutex

	credentials map[string]remoteConsumerCredential
	revoked     map[string]bool
	collection  map[achrix.Principal]bool
	rights      map[achrix.Principal]map[string]bool
	enabled     map[string]bool
	blocked     map[string]bool
	policyCause error
	policyCalls []remoteConsumerPolicyCall
	causes      []remoteConsumerCause
	events      []bridge.Diagnostic
	logs        safeBuffer
}

func newRemoteConsumer(t *testing.T) *remoteConsumer {
	t.Helper()
	f := &remoteConsumer{
		urls: map[string]string{}, tokens: map[string]string{},
		credentials: map[string]remoteConsumerCredential{}, revoked: map[string]bool{},
		collection: map[achrix.Principal]bool{"alice": true, "bob": true},
		rights: map[achrix.Principal]map[string]bool{
			"alice": {"site005": true, "site130": true, "site239": true},
			"bob":   {},
		},
		enabled: map[string]bool{"http": true, "mcp": true}, blocked: map[string]bool{},
	}
	for i := 180; i <= 200; i++ {
		f.rights["bob"][fmt.Sprintf("site%03d", i)] = true
	}
	var sites []multisite.Site
	for i := 0; i < 240; i++ {
		id := fmt.Sprintf("site%03d", i)
		sites = append(sites, multisite.Site{ID: multisite.SiteID(id),
			Authorities: []string{id + ".private.example.test"}, Disabled: i == 130})
	}
	module, err := multisite.NewManaged(multisite.Config{Sites: sites}, multisite.ManagementConfig{
		Available: []achrix.Descriptor{{ID: "example.notes", Version: "consumer-proof"}},
		Sites:     []multisite.SiteSelection{{ID: "site005", Modules: []string{"example.notes"}, Generation: 9007199254740993}},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(ctx context.Context, principal achrix.Principal, capability, target string) error {
		request, _ := bridge.RequestFromContext(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.policyCalls = append(f.policyCalls, remoteConsumerPolicyCall{principal, target, request})
		if f.policyCause != nil {
			return f.policyCause
		}
		if capability != multisite.InventoryRead {
			return achrix.ErrDenied
		}
		if target == multisite.InventoryTarget && f.collection[principal] || target != multisite.InventoryTarget && f.rights[principal][target] {
			return nil
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{
		StartupTimeout: time.Second, ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewJSONHandler(&f.logs, nil)),
		Diagnostic: func(ctx context.Context, operation, phase string, cause error) {
			request, _ := bridge.RequestFromContext(ctx)
			f.mu.Lock()
			f.causes = append(f.causes, remoteConsumerCause{request, operation, phase, cause})
			f.mu.Unlock()
		},
	}, policy, module)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = multisite.NewService(app, module)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	mux := http.NewServeMux()
	f.server = httptest.NewTLSServer(mux)
	t.Cleanup(f.server.Close)
	f.client = f.server.Client()
	f.client.Timeout = 4 * time.Second
	t.Cleanup(f.client.CloseIdleConnections)
	f.urls["http"], f.urls["mcp"] = f.server.URL+"/api/multisite/v1", f.server.URL+"/mcp"
	for _, channel := range []string{"http", "mcp"} {
		for _, principal := range []achrix.Principal{"alice", "bob"} {
			token := rand.Text() + rand.Text()
			f.tokens[channel+"/"+string(principal)] = token
			f.credentials[token] = remoteConsumerCredential{principal, f.urls[channel]}
		}
	}
	for _, channel := range []string{"http", "mcp"} {
		cfg := bridge.Config{
			ResourceURL: f.urls[channel], AuthorizationServers: []string{"https://identity.example.test"}, Scopes: []string{"sites"},
			Verify: func(_ context.Context, token string) (bridge.Credential, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				credential, exists := f.credentials[token]
				if !exists || f.revoked[token] || credential.audience != f.urls[channel] {
					return bridge.Credential{}, bridge.ErrInvalidCredential
				}
				return bridge.Credential{
					Caller:    bridge.Caller{Principal: credential.principal, ClientID: "notes-" + channel, ConnectionID: channel + "-" + string(credential.principal)},
					ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"sites"},
				}, nil
			},
			Admit: func(_ context.Context, _ bridge.Caller) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if !f.enabled[channel] {
					return achrix.ErrDenied
				}
				return nil
			},
			Gate: func(_ context.Context, _ bridge.Caller, operation string) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.blocked[channel+"/"+operation] {
					return achrix.ErrDenied
				}
				return nil
			},
			Diagnostic: func(_ context.Context, event bridge.Diagnostic) {
				f.mu.Lock()
				f.events = append(f.events, event)
				f.mu.Unlock()
			},
			Limits: bridge.Limits{RequestBytes: 4096, ResponseBytes: 16384, Concurrent: 4, Timeout: 2 * time.Second},
		}
		var endpoint *bridge.Endpoint
		if channel == "http" {
			endpoint, err = remote.NewHTTP(f.service, cfg)
			if err == nil {
				mux.Handle("/api/multisite/v1/", endpoint)
			}
		} else {
			endpoint, err = remote.NewMCP(f.service, cfg)
			if err == nil {
				mux.Handle("/mcp", endpoint)
			}
		}
		if err != nil {
			t.Fatal("compose public Multi-Site adapter:", err)
		}
	}
	return f
}

type remoteConsumerFailure struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	RequestID string            `json:"request_id"`
}

type remoteConsumerReply struct {
	status    int
	requestID string
	raw       []byte
	data      json.RawMessage
	failure   remoteConsumerFailure
}

func (f *remoteConsumer) call(t *testing.T, channel, token, operation string, arguments map[string]any) remoteConsumerReply {
	t.Helper()
	method, address, body := http.MethodGet, f.urls[channel], ""
	if channel == "http" {
		query := url.Values{}
		address += "/sites"
		if operation == remote.SiteOperation {
			id, _ := arguments["site_id"].(string)
			address += "/" + url.PathEscape(id)
		}
		for key, value := range arguments {
			if operation == remote.SiteOperation && key == "site_id" {
				continue
			}
			query.Set(key, fmt.Sprint(value))
		}
		if len(query) != 0 {
			address += "?" + query.Encode()
		}
	} else {
		method = http.MethodPost
		encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name": operation, "arguments": arguments, "_meta": map[string]any{
				mcp.MetaKeyProtocolVersion: "2026-07-28", mcp.MetaKeyClientCapabilities: map[string]any{},
				mcp.MetaKeyClientInfo: map[string]any{"name": "FORGED_CLIENT", "version": "1"},
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		body = string(encoded)
	}
	request, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Actor", "alice")
	request.Header.Set("X-Site-ID", "site005")
	request.Header.Set("X-Client-ID", "FORGED_CLIENT")
	request.Header.Set("X-Connection-ID", "FORGED_CONNECTION")
	request.Header.Set("X-Request-ID", "FORGED_REQUEST")
	if channel == "mcp" {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Mcp-Protocol-Version", "2026-07-28")
		request.Header.Set("Mcp-Method", "tools/call")
		request.Header.Set("Mcp-Name", operation)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal("actual HTTPS consumer request:", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 {
		t.Fatal("response exceeded the configured consumer bound:", err)
	}
	reply := remoteConsumerReply{status: response.StatusCode, requestID: response.Header.Get("X-Request-ID"), raw: raw}
	if reply.requestID == "" || reply.requestID == "FORGED_REQUEST" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing trusted correlation or private response policy", response.StatusCode, response.Header)
	}
	if response.StatusCode != http.StatusOK {
		if err := json.Unmarshal(raw, &reply.failure); err != nil {
			t.Fatal("missing safe HTTP problem:", err, string(raw))
		}
		return reply
	}
	if channel == "http" {
		reply.data = raw
		return reply
	}
	var envelope struct {
		Result struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Error) != 0 {
		t.Fatal("unexpected MCP protocol failure:", err, string(raw))
	}
	if envelope.Result.IsError {
		if len(envelope.Result.Content) != 1 || json.Unmarshal([]byte(envelope.Result.Content[0].Text), &reply.failure) != nil {
			t.Fatal("missing safe MCP operation failure", string(raw))
		}
	} else {
		reply.data = envelope.Result.StructuredContent
	}
	return reply
}

func (f *remoteConsumer) policyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.policyCalls)
}

func remoteConsumerPage(t *testing.T, reply remoteConsumerReply) remote.InventoryPage {
	t.Helper()
	var page remote.InventoryPage
	if reply.status != http.StatusOK || reply.failure.Code != "" || json.Unmarshal(reply.data, &page) != nil || page.Sites == nil {
		t.Fatal("invalid public inventory result", string(reply.raw))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reply.data, &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		if key != "sites" && key != "next_cursor" {
			t.Fatal("inventory exposed metadata outside its permitted result contract", key)
		}
	}
	for _, site := range page.Sites {
		if site.Modules == nil || len(site.Revision) != 64 {
			t.Fatal("public site metadata lost arrays or exact revision", site)
		}
	}
	return page
}

func remoteConsumerSite(t *testing.T, reply remoteConsumerReply) remote.SiteInfo {
	t.Helper()
	var site remote.SiteInfo
	if reply.status != http.StatusOK || reply.failure.Code != "" || json.Unmarshal(reply.data, &site) != nil || site.ID == "" || site.Modules == nil || len(site.Revision) != 64 {
		t.Fatal("invalid public exact-site result", string(reply.raw))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reply.data, &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		if key != "id" && key != "disabled" && key != "modules" && key != "revision" {
			t.Fatal("exact-site read exposed metadata outside its permitted result contract", key)
		}
	}
	return site
}

func remoteConsumerDenied(t *testing.T, reply remoteConsumerReply, code string) {
	t.Helper()
	if reply.failure.Code != code || reply.failure.Message == "" || reply.failure.Action == "" || reply.failure.RequestID != reply.requestID || reply.failure.Outcome != bridge.NotExecuted {
		t.Fatal("failure lost safe useful meaning or trusted correlation", string(reply.raw))
	}
}

func TestPublicMultiSiteRemoteSparseTraversalAndCurrentPermissions(t *testing.T) {
	for _, channel := range []string{"http", "mcp"} {
		t.Run(channel, func(t *testing.T) {
			f := newRemoteConsumer(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			direct, err := f.service.Inventory(ctx, "alice", "", 1)
			if err != nil || len(direct.Sites) != 1 || direct.Sites[0].ID != "site005" {
				t.Fatal("direct owning Service inventory:", direct, err)
			}
			before := f.policyCount()
			firstReply := f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{"limit": 1})
			first := remoteConsumerPage(t, firstReply)
			if len(first.Sites) != 1 || first.Sites[0].ID != string(direct.Sites[0].ID) || first.Sites[0].Revision != direct.Sites[0].Revision || first.NextCursor != direct.NextCursor {
				t.Fatal("adapter diverged from the owning sparse Service", first, direct)
			}
			if f.policyCount()-before <= 100 {
				t.Fatal("consumer never traversed the more-than-100 denied-site gap")
			}
			decoded, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
			if err != nil || string(decoded) != "site005" || strings.Contains(string(firstReply.raw), "site006") || strings.Contains(string(firstReply.raw), ".private.example.test") {
				t.Fatal("discovery exposed hidden identities or private routing")
			}
			second := remoteConsumerPage(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{"cursor": first.NextCursor, "limit": 1}))
			if len(second.Sites) != 1 || second.Sites[0].ID != "site130" || !second.Sites[0].Disabled || second.NextCursor == "" {
				t.Fatal("sparse traversal stopped early or lost disabled metadata", second)
			}
			f.mu.Lock()
			f.rights["alice"]["site239"] = false
			f.mu.Unlock()
			afterRevocation := remoteConsumerPage(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{"cursor": second.NextCursor, "limit": 1}))
			if len(afterRevocation.Sites) != 0 || afterRevocation.NextCursor != "" {
				t.Fatal("next page reused a prior exact-site grant", afterRevocation)
			}
			f.mu.Lock()
			f.collection["alice"] = false
			f.mu.Unlock()
			remoteConsumerDenied(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{"cursor": first.NextCursor, "limit": 1}), "denied")
			// Revoking collection discovery does not invent a new requirement on
			// the Service's independently authorized exact-site operation.
			site := remoteConsumerSite(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"}))
			if site.ID != "site005" || !reflect.DeepEqual(site.Modules, []string{"example.notes"}) {
				t.Fatal("exact-site permission was incorrectly coupled to collection discovery", site)
			}
			bob := remoteConsumerPage(t, f.call(t, channel, f.tokens[channel+"/bob"], remote.InventoryOperation, map[string]any{}))
			if len(bob.Sites) != 20 || bob.Sites[0].ID != "site180" || bob.Sites[19].ID != "site199" || bob.NextCursor == "" {
				t.Fatal("omitted limit did not use the bounded default or borrowed another user's rights", bob)
			}
			last := remoteConsumerPage(t, f.call(t, channel, f.tokens[channel+"/bob"], remote.InventoryOperation, map[string]any{"cursor": bob.NextCursor}))
			if len(last.Sites) != 1 || last.Sites[0].ID != "site200" || last.NextCursor != "" {
				t.Fatal("consumer failed to finish bounded inventory", last)
			}
			for _, limit := range []int{0, 101} {
				before := f.policyCount()
				invalid := f.call(t, channel, f.tokens[channel+"/bob"], remote.InventoryOperation, map[string]any{"limit": limit})
				remoteConsumerDenied(t, invalid, "invalid_arguments")
				if f.policyCount() != before || len(invalid.failure.Fields) == 0 {
					t.Fatal("invalid bounds reached the Service or lost field guidance")
				}
			}
		})
	}
}

func TestPublicMultiSiteRemoteCannotManufactureAuthority(t *testing.T) {
	for _, channel := range []string{"http", "mcp"} {
		t.Run(channel, func(t *testing.T) {
			f := newRemoteConsumer(t)
			// call always supplies forged actor/site/client/correlation headers;
			// current verified Bob still cannot read Alice's exact site.
			existing := f.call(t, channel, f.tokens[channel+"/bob"], remote.SiteOperation, map[string]any{"site_id": "site005"})
			missing := f.call(t, channel, f.tokens[channel+"/bob"], remote.SiteOperation, map[string]any{"site_id": "site500"})
			remoteConsumerDenied(t, existing, "denied")
			remoteConsumerDenied(t, missing, "denied")
			if existing.status != missing.status || existing.failure.Message != missing.failure.Message || existing.failure.Action != missing.failure.Action || !reflect.DeepEqual(existing.failure.Fields, missing.failure.Fields) {
				t.Fatal("ungranted exact-site read revealed existence")
			}
			before := f.policyCount()
			for _, claim := range []string{"actor", "client_id", "connection_id"} {
				remoteConsumerDenied(t, f.call(t, channel, f.tokens[channel+"/bob"], remote.SiteOperation, map[string]any{"site_id": "site005", claim: "alice"}), "invalid_arguments")
			}
			if f.policyCount() != before {
				t.Fatal("caller-supplied identity argument reached domain policy")
			}
			remoteConsumerSite(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"}))
			f.mu.Lock()
			f.rights["alice"]["site005"] = false
			f.mu.Unlock()
			remoteConsumerDenied(t, f.call(t, channel, f.tokens[channel+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"}), "denied")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := f.service.Site(ctx, "alice", "site005"); !errors.Is(err, achrix.ErrDenied) {
				t.Fatal("direct and remote callers did not share current policy", err)
			}
			f.mu.Lock()
			f.rights["alice"]["site500"] = true
			f.mu.Unlock()
			authorizedMissing := f.call(t, channel, f.tokens[channel+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site500"})
			if authorizedMissing.failure.Code != "site_not_found" || authorizedMissing.failure.Action == "" {
				t.Fatal("authorized exact absence lost useful meaning", string(authorizedMissing.raw))
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, call := range f.policyCalls {
				if call.request.ID == "" { // Explicit direct Service call above.
					continue
				}
				if call.request.Caller.Principal != call.principal || call.request.Caller.ClientID != "notes-"+channel || call.request.Caller.ConnectionID != channel+"-"+string(call.principal) || call.request.Channel != channel || call.request.Operation != remote.SiteOperation || call.request.ID == "FORGED_REQUEST" {
					t.Fatal("untrusted input replaced verified request attribution", call)
				}
			}
		})
	}
}

func TestPublicMultiSiteRemoteAudienceRevocationAndChannelNarrowing(t *testing.T) {
	f := newRemoteConsumer(t)
	for _, channel := range []string{"http", "mcp"} {
		other := "mcp"
		if channel == "mcp" {
			other = "http"
		}
		before := f.policyCount()
		wrongAudience := f.call(t, channel, f.tokens[other+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"})
		remoteConsumerDenied(t, wrongAudience, "invalid_credential")
		if wrongAudience.status != http.StatusUnauthorized || f.policyCount() != before {
			t.Fatal("credential issued for the sibling audience reached domain policy")
		}
	}
	f.mu.Lock()
	f.blocked["mcp/"+remote.SiteOperation] = true
	f.mu.Unlock()
	before := f.policyCount()
	remoteConsumerDenied(t, f.call(t, "mcp", f.tokens["mcp/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"}), "denied")
	if f.policyCount() != before {
		t.Fatal("channel gate did not reject before dispatch")
	}
	remoteConsumerSite(t, f.call(t, "http", f.tokens["http/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if site, err := f.service.Site(ctx, "alice", "site005"); err != nil || site.ID != "site005" {
		t.Fatal("MCP restriction changed direct product authority", err)
	}
	for _, channel := range []string{"http", "mcp"} {
		f.mu.Lock()
		f.enabled[channel] = false
		f.mu.Unlock()
		before := f.policyCount()
		disabled := f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{})
		remoteConsumerDenied(t, disabled, "denied")
		if disabled.status != http.StatusForbidden || f.policyCount() != before {
			t.Fatal("administrator disablement did not stop the next request")
		}
		f.mu.Lock()
		f.enabled[channel] = true
		f.revoked[f.tokens[channel+"/alice"]] = true
		f.mu.Unlock()
		revoked := f.call(t, channel, f.tokens[channel+"/alice"], remote.InventoryOperation, map[string]any{})
		remoteConsumerDenied(t, revoked, "invalid_credential")
		if revoked.status != http.StatusUnauthorized || f.policyCount() != before {
			t.Fatal("grant revocation did not stop the next request")
		}
		// Revoking Alice's one resource-bound grant does not revoke Bob.
		remoteConsumerPage(t, f.call(t, channel, f.tokens[channel+"/bob"], remote.InventoryOperation, map[string]any{"limit": 1}))
	}
}

func TestPublicMultiSiteRemoteProtectedPolicyDiagnostics(t *testing.T) {
	f := newRemoteConsumer(t)
	privateCause := errors.New("PRIVATE_POLICY_PROVIDER_DETAIL")
	f.mu.Lock()
	f.policyCause = privateCause
	f.mu.Unlock()
	for _, channel := range []string{"http", "mcp"} {
		reply := f.call(t, channel, f.tokens[channel+"/alice"], remote.SiteOperation, map[string]any{"site_id": "site005"})
		remoteConsumerDenied(t, reply, "authorization_unavailable")
		if channel == "http" && reply.status != http.StatusServiceUnavailable {
			t.Fatal("authorization outage was misreported as a permission denial")
		}
		f.mu.Lock()
		var original *remoteConsumerCause
		for i := range f.causes {
			if f.causes[i].request.ID == reply.requestID {
				value := f.causes[i]
				original = &value
			}
		}
		var terminal *bridge.Diagnostic
		for i := range f.events {
			if f.events[i].Request.ID == reply.requestID {
				value := f.events[i]
				terminal = &value
			}
		}
		f.mu.Unlock()
		if original == nil || original.cause != privateCause || original.operation != "authorize" || original.phase != "policy" {
			t.Fatal("Core did not retain the original protected policy cause", original)
		}
		if terminal == nil || !errors.Is(terminal.Cause, achrix.ErrAuthorizationUnavailable) || !terminal.Dispatched || terminal.Outcome != bridge.NotExecuted || terminal.Code != "authorization_unavailable" {
			t.Fatal("transport lost truthful owning-service failure evidence", terminal)
		}
		if terminal.Request != original.request || terminal.Request.Caller.Principal != "alice" || terminal.Request.Caller.ClientID != "notes-"+channel || terminal.Request.Channel != channel || terminal.Request.Operation != remote.SiteOperation {
			t.Fatal("protected diagnostic boundaries lost trusted attribution")
		}
		for _, secret := range []string{privateCause.Error(), f.tokens[channel+"/alice"], "FORGED_REQUEST", "FORGED_CLIENT", "FORGED_CONNECTION"} {
			if strings.Contains(string(reply.raw), secret) || strings.Contains(f.logs.String(), secret) {
				t.Fatal("private or untrusted evidence entered public output/ordinary logs")
			}
		}
	}
	if logs := f.logs.String(); !strings.Contains(logs, "policy_evaluation_failed") || !strings.Contains(logs, `"phase":"policy"`) {
		t.Fatal("safe ordinary diagnostics suppressed the useful failure category", logs)
	}
}

type remoteConsumerSDKTransport struct {
	base        http.RoundTripper
	token       string
	getRequests atomic.Int32
}

func (t *remoteConsumerSDKTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		t.getRequests.Add(1)
	}
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	copy.Header.Set("X-Actor", "bob")
	return t.base.RoundTrip(copy)
}

func TestPublicMultiSiteRemoteOfficialSDK(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := newRemoteConsumer(t)
			transport := &remoteConsumerSDKTransport{base: f.client.Transport, token: f.tokens["mcp/alice"]}
			httpClient := *f.client
			httpClient.Transport = transport
			client := mcp.NewClient(&mcp.Implementation{Name: "notes-multisite-consumer", Version: "1"}, &mcp.ClientOptions{
				Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
				Endpoint: f.urls["mcp"], HTTPClient: &httpClient, DisableStandaloneSSE: true, MaxRetries: -1,
			}, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal("official SDK connection:", err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			if result := session.InitializeResult(); result == nil || result.ProtocolVersion != version {
				t.Fatal("SDK silently negotiated another protocol")
			}
			catalog, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil || len(catalog.Tools) != 2 {
				t.Fatal("bounded public catalog:", catalog, err)
			}
			seen := map[string]bool{}
			for _, tool := range catalog.Tools {
				seen[tool.Name] = true
				if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
					t.Fatal("discovery lacks typed schemas or read-only effect metadata", tool.Name)
				}
			}
			if !seen[remote.InventoryOperation] || !seen[remote.SiteOperation] {
				t.Fatal("unexpected public operation catalog", seen)
			}
			limit := 1
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: remote.InventoryOperation, Arguments: remote.InventoryInput{Limit: &limit}})
			if err != nil || result.IsError {
				t.Fatal("official SDK inventory:", err, result)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var page remote.InventoryPage
			if json.Unmarshal(encoded, &page) != nil || len(page.Sites) != 1 || page.Sites[0].ID != "site005" || page.NextCursor == "" {
				t.Fatal("official SDK lost the typed sparse inventory", string(encoded))
			}
			result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: remote.SiteOperation, Arguments: remote.SiteInput{SiteID: page.Sites[0].ID}})
			if err != nil || result.IsError {
				t.Fatal("official SDK exact-site read:", err, result)
			}
			encoded, err = json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var site remote.SiteInfo
			if json.Unmarshal(encoded, &site) != nil || site.ID != page.Sites[0].ID || site.Revision != page.Sites[0].Revision {
				t.Fatal("official SDK altered the exact revision", string(encoded))
			}
			if transport.getRequests.Load() != 0 {
				t.Fatal("request-only consumer opened an idle SSE stream")
			}
			f.mu.Lock()
			f.rights["alice"]["site005"] = false
			f.mu.Unlock()
			result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: remote.SiteOperation, Arguments: remote.SiteInput{SiteID: site.ID}})
			if err != nil || !result.IsError {
				t.Fatal("SDK discovery froze old product authority:", err, result)
			}
		})
	}
}
