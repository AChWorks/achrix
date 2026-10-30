// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	auditremote "github.com/AChWorks/achrix/audit/remote"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
	identityremote "github.com/AChWorks/achrix/identity/remote"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const identityAuditRemoteRevision int64 = 9007199254740993

type identityAuditRemoteCredential struct {
	principal         achrix.Principal
	audience, session string
	expires           time.Time
}
type identityAuditRemoteCause struct {
	owner, operation, phase string
	request                 bridge.Request
	cause                   error
}
type identityAuditRemotePolicyCall struct {
	actor              achrix.Principal
	capability, target string
	request            bridge.Request
}
type identityAuditRemoteFixture struct {
	app                     *achrix.Application
	identity                *identity.Service
	audit                   *audit.Service
	admin, pool             *pgxpool.Pool
	database                string
	accounts                map[string]identity.Account
	sessions                map[string]identity.Session
	server                  *httptest.Server
	client                  *http.Client
	urls, tokens            map[string]string
	mu                      sync.Mutex
	credentials             map[string]identityAuditRemoteCredential
	grants                  map[achrix.Principal]map[string]bool
	enabled, blocked        map[string]bool
	policyFailureCapability string
	policyCause             error
	policyCalls             []identityAuditRemotePolicyCall
	causes                  []identityAuditRemoteCause
	events                  []bridge.Diagnostic
	logs                    safeBuffer
}

// This fixture composes the actual services around one current product policy.
// Fixture resource credentials bind real Identity sessions and independent
// audiences; they are not an OAuth issuer or hosted-client onboarding proof.
func newIdentityAuditRemoteFixture(t *testing.T) *identityAuditRemoteFixture {
	t.Helper()
	dsn := os.Getenv("NOTES_IDENTITY_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOTES_IDENTITY_DATABASE_URL is required; full validate.sh executes this real PostgreSQL proof")
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil || base.ConnConfig.Database != "achrix_identity_consumer" ||
		!filepath.IsAbs(base.ConnConfig.Host) || filepath.Base(base.ConnConfig.Host) != "socket" ||
		!strings.HasPrefix(filepath.Dir(base.ConnConfig.Host), "/tmp/achrix-validation.") || base.ConnConfig.TLSConfig != nil {
		t.Fatal("the controlled local NOTES_IDENTITY_DATABASE_URL validation cluster is required")
	}
	info, err := os.Lstat(filepath.Dir(base.ConnConfig.Host))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("Identity/Audit remote proof requires the private task-owned validation cluster")
	}
	for _, fallback := range base.ConnConfig.Fallbacks {
		if fallback == nil || fallback.Host != base.ConnConfig.Host || fallback.Port != base.ConnConfig.Port || fallback.TLSConfig != nil {
			t.Fatal("Identity/Audit remote proof cannot create databases through another fallback host")
		}
	}
	f := &identityAuditRemoteFixture{
		accounts: map[string]identity.Account{}, sessions: map[string]identity.Session{},
		urls: map[string]string{}, tokens: map[string]string{}, credentials: map[string]identityAuditRemoteCredential{},
		grants: map[achrix.Principal]map[string]bool{}, enabled: map[string]bool{}, blocked: map[string]bool{},
	}
	base.MaxConns = 1
	base.ConnConfig.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f.admin, err = pgxpool.NewWithConfig(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.close(t) })
	var actual, encoding string
	var version int
	if err := f.admin.QueryRow(ctx, "SELECT current_database(),current_setting('server_version_num')::integer,current_setting('server_encoding')").Scan(&actual, &version, &encoding); err != nil || actual != "achrix_identity_consumer" || version != 180006 || encoding != "UTF8" {
		t.Fatal("Identity/Audit remote validation cluster identity/profile mismatch", err)
	}
	name := "achrix_identity_remote_" + strings.ToLower(rand.Text())
	if _, err := f.admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		t.Fatal("create owned Identity/Audit database:", err)
	}
	f.database = name // Cleanup owns only the exact successfully created database.
	connection := &url.URL{Scheme: "postgresql", Host: "localhost", Path: "/" + name, User: url.UserPassword(base.ConnConfig.User, base.ConnConfig.Password)}
	connection.RawQuery = url.Values{"host": {base.ConnConfig.Host}, "port": {strconv.Itoa(int(base.ConnConfig.Port))}, "sslmode": {"disable"}}.Encode()
	privateDSN := connection.String()
	if err := audit.Migrate(ctx, privateDSN); err != nil {
		t.Fatal(err)
	}
	if err := identity.Migrate(ctx, privateDSN); err != nil {
		t.Fatal(err)
	}
	queryConfig, err := pgxpool.ParseConfig(privateDSN)
	if err != nil {
		t.Fatal(err)
	}
	queryConfig.MaxConns = 2
	queryConfig.ConnConfig.ConnectTimeout = time.Second
	f.pool, err = pgxpool.NewWithConfig(ctx, queryConfig)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(&f.logs, nil))
	capture := func(owner string) func(context.Context, string, string, error) {
		return func(ctx context.Context, operation, phase string, cause error) {
			request, _ := bridge.RequestFromContext(ctx)
			f.mu.Lock()
			f.causes = append(f.causes, identityAuditRemoteCause{owner, operation, phase, request, cause})
			f.mu.Unlock()
		}
	}
	auditModule, err := audit.NewPostgres(privateDSN, audit.Config{MaxConns: 2, MaxOperations: 4, Diagnostic: capture("audit")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	identityModule, err := identity.NewPostgres(privateDSN, identity.Config{MaxConns: 2, MaxOperations: 4, Diagnostic: capture("identity")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(ctx context.Context, actor achrix.Principal, capability, target string) error {
		request, _ := bridge.RequestFromContext(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.policyCalls = append(f.policyCalls, identityAuditRemotePolicyCall{actor, capability, target, request})
		if actor == identity.PublicPrincipal && capability == identity.Authentication && target == "" {
			return nil
		}
		// Only trusted fixture setup has this principal; no resource credential
		// is ever issued for it and remote arguments cannot select it.
		if actor == fixtureAdministrator && (capability == identity.AccountCreate || capability == audit.Append) {
			return nil
		}
		if capability == f.policyFailureCapability && f.policyCause != nil {
			return f.policyCause
		}
		if f.grants[actor][capability+"\x00"+target] {
			return nil
		}
		return achrix.ErrDenied
	})
	f.app, err = achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second, Logger: logger, Diagnostic: capture("core")}, policy, identityModule, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	f.audit, err = audit.NewService(f.app, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	f.identity, err = identity.NewService(f.app, identityModule, f.audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob"} {
		account, err := f.identity.CreateAccount(ctx, fixtureAdministrator, "remote-"+user, fixtureInitialPassword)
		if err != nil {
			t.Fatal("create synthetic fixture account:", err)
		}
		f.accounts[user] = account
	}
	// Establish the large revision BEFORE Login. Updating it afterward would
	// invalidate the stored session revision and falsely prove later revocation.
	if _, err := f.pool.Exec(ctx, "UPDATE identity.accounts SET revision=$2 WHERE id=$1", f.accounts["bob"].ID, identityAuditRemoteRevision); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob"} {
		session, err := f.identity.Login(ctx, "remote-"+user, fixtureInitialPassword, "")
		if err != nil {
			t.Fatal("issue real synthetic fixture session:", err)
		}
		f.sessions[user] = session
	}
	target := f.accounts["bob"].ID
	for _, capability := range []string{identity.AccountRead, identity.AccountSetEnabled, audit.Append, audit.Query, audit.Export} {
		f.grant("alice", capability, target, true)
	}
	f.grant("bob", identity.AccountRead, target, true)
	f.grant("bob", audit.Query, target, true)
	mux := http.NewServeMux()
	f.server = httptest.NewTLSServer(mux)
	f.client = f.server.Client()
	f.client.Timeout = 6 * time.Second
	for _, component := range []string{"identity", "audit"} {
		for _, channel := range []string{"http", "mcp"} {
			key := component + "-" + channel
			path := "/api/" + component + "/v1"
			if channel == "mcp" {
				path = "/mcp/" + component
			}
			f.urls[key] = f.server.URL + path
			f.enabled[key] = true
			for _, user := range []string{"alice", "bob"} {
				token := rand.Text() + rand.Text()
				f.tokens[key+"/"+user] = token
				f.credentials[token] = identityAuditRemoteCredential{f.sessions[user].Principal, f.urls[key], f.sessions[user].Token, f.sessions[user].ExpiresAt}
			}
			cfg := bridge.Config{
				ResourceURL: f.urls[key], AuthorizationServers: []string{"https://identity.example.test"}, Scopes: []string{component},
				Verify: func(ctx context.Context, token string) (bridge.Credential, error) {
					f.mu.Lock()
					credential, exists := f.credentials[token]
					f.mu.Unlock()
					if !exists || credential.audience != f.urls[key] {
						return bridge.Credential{}, bridge.ErrInvalidCredential
					}
					actor, err := f.identity.Authenticate(ctx, credential.session)
					if errors.Is(err, identity.ErrAuthentication) {
						return bridge.Credential{}, bridge.ErrInvalidCredential
					}
					if err != nil {
						return bridge.Credential{}, err
					}
					if actor != credential.principal {
						return bridge.Credential{}, bridge.ErrInvalidCredential
					}
					return bridge.Credential{Caller: bridge.Caller{Principal: actor, ClientID: "notes-" + key, ConnectionID: key + "-" + string(actor)}, ExpiresAt: credential.expires, Scopes: []string{component}}, nil
				},
				Admit: func(context.Context, bridge.Caller) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if !f.enabled[key] {
						return achrix.ErrDenied
					}
					return nil
				},
				Gate: func(_ context.Context, _ bridge.Caller, operation string) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.blocked[key+"/"+operation] {
						return achrix.ErrDenied
					}
					return nil
				},
				Diagnostic: func(_ context.Context, event bridge.Diagnostic) {
					f.mu.Lock()
					f.events = append(f.events, event)
					f.mu.Unlock()
				},
				Limits: bridge.Limits{Concurrent: 4, Timeout: 4 * time.Second},
			}
			var endpoint *bridge.Endpoint
			if component == "identity" {
				if channel == "http" {
					endpoint, err = identityremote.NewHTTP(f.identity, cfg)
				} else {
					endpoint, err = identityremote.NewMCP(f.identity, cfg)
				}
			} else {
				if channel == "http" {
					endpoint, err = auditremote.NewHTTP(f.audit, cfg)
				} else {
					endpoint, err = auditremote.NewMCP(f.audit, cfg)
				}
			}
			if err != nil {
				t.Fatal("compose public Identity/Audit adapter:", err)
			}
			if channel == "http" {
				path += "/"
			}
			mux.Handle(path, endpoint)
		}
	}
	return f
}

func (f *identityAuditRemoteFixture) close(t *testing.T) {
	t.Helper()
	if f.admin != nil {
		defer f.admin.Close()
	}
	if f.client != nil {
		f.client.CloseIdleConnections()
	}
	if f.server != nil {
		f.server.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var shutdown error
	if f.app != nil {
		shutdown = f.app.Shutdown(ctx)
	}
	if f.pool != nil {
		f.pool.Close()
	}
	if shutdown != nil {
		t.Errorf("Identity/Audit shutdown unverified; retaining owned database %s: %v", f.database, shutdown)
		return
	}
	if f.database != "" {
		if _, err := f.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{f.database}.Sanitize()); err != nil {
			t.Errorf("Identity/Audit cleanup failed; retaining owned database %s: %v", f.database, err)
		}
	}
}
func (f *identityAuditRemoteFixture) grant(user, capability, target string, allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	actor := achrix.Principal(f.accounts[user].ID)
	if f.grants[actor] == nil {
		f.grants[actor] = map[string]bool{}
	}
	f.grants[actor][capability+"\x00"+target] = allowed
}
func (f *identityAuditRemoteFixture) countCalls(capability string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.policyCalls {
		if call.capability == capability {
			count++
		}
	}
	return count
}
func (f *identityAuditRemoteFixture) exec(t *testing.T, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx, sql); err != nil {
		t.Fatal("owned fixture SQL:", err)
	}
}

type identityAuditRemoteState struct {
	enabled           bool
	revision          int64
	sessions, records int
}

func (f *identityAuditRemoteFixture) state(t *testing.T) identityAuditRemoteState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var state identityAuditRemoteState
	if err := f.pool.QueryRow(ctx, "SELECT enabled,revision,(SELECT count(*) FROM identity.sessions WHERE account_id=$1),(SELECT count(*) FROM audit.records WHERE target=$1) FROM identity.accounts WHERE id=$1", f.accounts["bob"].ID).Scan(&state.enabled, &state.revision, &state.sessions, &state.records); err != nil {
		t.Fatal(err)
	}
	return state
}
func (f *identityAuditRemoteFixture) requireSession(t *testing.T, valid bool) {
	t.Helper()
	actor, err := f.identity.Authenticate(context.Background(), f.sessions["bob"].Token)
	if valid && (err != nil || actor != f.sessions["bob"].Principal) || !valid && !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("actual session validity differs from committed Identity state", valid, err)
	}
}

type identityAuditRemoteFault struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	Reference string            `json:"reference"`
	RequestID string            `json:"request_id"`
}
type identityAuditRemoteResponse struct {
	status int
	header http.Header
	data   []byte
}

func identityAuditRemoteDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal("public Identity/Audit JSON:", err)
	}
	return value
}
func (f *identityAuditRemoteFixture) request(t *testing.T, endpoint, token, method, path string, input any) identityAuditRemoteResponse {
	t.Helper()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, f.urls[endpoint]+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Actor", f.accounts["alice"].ID)
	request.Header.Set("X-Client-ID", "FORGED_IDENTITY_AUDIT_CLIENT")
	request.Header.Set("X-Request-ID", "FORGED_IDENTITY_AUDIT_REQUEST")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal("actual HTTPS Identity/Audit request:", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		t.Fatal("bounded Identity/Audit response:", err)
	}
	if id := response.Header.Get("X-Request-ID"); id == "" || id == "FORGED_IDENTITY_AUDIT_REQUEST" {
		t.Fatal("missing trusted HTTP request identity")
	}
	return identityAuditRemoteResponse{response.StatusCode, response.Header.Clone(), data}
}
func identityAuditRemoteHTTPFault(t *testing.T, response identityAuditRemoteResponse) identityAuditRemoteFault {
	t.Helper()
	fault := identityAuditRemoteDecode[identityAuditRemoteFault](t, response.data)
	if response.status < 400 || fault.Code == "" || fault.Message == "" || fault.Action == "" || fault.RequestID != response.header.Get("X-Request-ID") {
		t.Fatal("missing actionable correlated HTTP failure", response.status, fault)
	}
	return fault
}
func (f *identityAuditRemoteFixture) session(t *testing.T, component, user string) *mcp.ClientSession {
	t.Helper()
	transport := &remoteConsumerSDKTransport{base: f.client.Transport, token: f.tokens[component+"-mcp/"+user]}
	httpClient := *f.client
	httpClient.Transport = transport
	client := mcp.NewClient(&mcp.Implementation{Name: "FORGED_IDENTITY_AUDIT_CLIENT", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.urls[component+"-mcp"], HTTPClient: &httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal("official Identity/Audit SDK connection:", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if transport.getRequests.Load() != 0 {
			t.Error("request-only SDK opened an idle SSE stream")
		}
	})
	catalog, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(catalog.Tools) != 2 {
		t.Fatal("bounded public Identity/Audit catalog", err)
	}
	want := map[string]bool{auditremote.QueryOperation: true, auditremote.ExportOperation: true}
	if component == "identity" {
		want = map[string]bool{identityremote.AccountOperation: true, identityremote.SetEnabledOperation: true}
	}
	for _, tool := range catalog.Tools {
		if !want[tool.Name] || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatal("catalog added an unassigned or untyped operation", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatal("public catalog omitted the selected workflow")
	}
	return session
}
func identityAuditRemoteCall(t *testing.T, session *mcp.ClientSession, operation string, arguments any) ([]byte, *identityAuditRemoteFault) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: operation, Arguments: arguments})
	if err != nil {
		t.Fatal("official Identity/Audit SDK operation:", err)
	}
	if result.IsError {
		if len(result.Content) != 1 {
			t.Fatal("missing safe tool failure")
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("tool failure is not safe structured text")
		}
		fault := identityAuditRemoteDecode[identityAuditRemoteFault](t, []byte(text.Text))
		if fault.Code == "" || fault.Message == "" || fault.Action == "" || fault.RequestID == "" {
			t.Fatal("missing actionable correlated tool failure", fault)
		}
		return []byte(text.Text), &fault
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, nil
}
func identityAuditRemoteSuccess[T any](t *testing.T, session *mcp.ClientSession, operation string, arguments any) T {
	t.Helper()
	data, fault := identityAuditRemoteCall(t, session, operation, arguments)
	if fault != nil {
		t.Fatal("unexpected Identity/Audit tool failure", fault)
	}
	return identityAuditRemoteDecode[T](t, data)
}
func identityAuditRemoteExpectFault(t *testing.T, session *mcp.ClientSession, operation string, arguments any, code string) identityAuditRemoteFault {
	t.Helper()
	_, fault := identityAuditRemoteCall(t, session, operation, arguments)
	if fault == nil || fault.Code != code {
		t.Fatal("unexpected Identity/Audit failure category", code, fault)
	}
	return *fault
}
func (f *identityAuditRemoteFixture) assertSafe(t *testing.T, data []byte) {
	t.Helper()
	secrets := []string{fixtureInitialPassword, f.database, "PRIVATE_IDENTITY_AUDIT_APPEND_CAUSE", "PRIVATE_IDENTITY_AUDIT_POLICY_CAUSE", "FORGED_IDENTITY_AUDIT_CLIENT", "FORGED_IDENTITY_AUDIT_REQUEST"}
	for _, session := range f.sessions {
		secrets = append(secrets, session.Token, session.CSRF)
	}
	for _, token := range f.tokens {
		secrets = append(secrets, token)
	}
	for _, secret := range secrets {
		if secret != "" && (strings.Contains(string(data), secret) || strings.Contains(f.logs.String(), secret)) {
			t.Fatal("private credential/cause/claimed attribution escaped into public or ordinary output")
		}
	}
}
func (f *identityAuditRemoteFixture) checkAttributedRecord(t *testing.T, page auditremote.Page, channel, requestID string) {
	t.Helper()
	found := 0
	for _, record := range page.Records {
		if record.Action != "identity.account.set-enabled" || record.Attribution == nil || record.Attribution.Channel != channel {
			continue
		}
		found++
		a := record.Attribution
		when, err := time.Parse(time.RFC3339Nano, record.OccurredAt)
		if string(record.Actor) != f.accounts["alice"].ID || record.Target != f.accounts["bob"].ID || record.Authority != identity.AccountSetEnabled || record.Outcome != "succeeded" || len(record.ID) != 26 || err != nil || when.Location() != time.UTC || a.ClientID != "notes-identity-"+channel || a.ConnectionID != "identity-"+channel+"-"+f.accounts["alice"].ID || a.Operation != identityremote.SetEnabledOperation || a.RequestID == "" || a.RequestID == "FORGED_IDENTITY_AUDIT_REQUEST" || requestID != "" && a.RequestID != requestID {
			t.Fatal("durable Audit lost actor-bound trusted mutation attribution", record)
		}
		f.mu.Lock()
		terminal := 0
		for _, event := range f.events {
			if event.Request.ID == a.RequestID && event.Request.Operation == a.Operation && event.Request.Channel == channel && string(event.Request.Caller.Principal) == string(record.Actor) && event.Request.Caller.ClientID == a.ClientID && event.Request.Caller.ConnectionID == a.ConnectionID && event.Dispatched && event.Outcome == bridge.Succeeded && event.Reference == record.Target {
				terminal++
			}
		}
		trustedPolicy := false
		for _, call := range f.policyCalls {
			if string(call.actor) == string(record.Actor) && call.capability == identity.AccountSetEnabled && call.target == record.Target && call.request.ID == a.RequestID && call.request.Operation == a.Operation && call.request.Channel == channel && call.request.Caller.ClientID == a.ClientID {
				trustedPolicy = true
			}
		}
		f.mu.Unlock()
		if terminal != 1 || !trustedPolicy {
			t.Fatal("durable Audit is not correlated with actual product policy and exactly one acknowledged Bridge mutation", terminal, trustedPolicy)
		}
	}
	if found != 1 {
		t.Fatal("successful status change did not retain exactly one attributed Audit event", channel, found)
	}
}

func TestPublicIdentityAuditRemoteLifecycleAndAtomicAttribution(t *testing.T) {
	f := newIdentityAuditRemoteFixture(t)
	alice, bob := f.session(t, "identity", "alice"), f.session(t, "identity", "bob")
	auditClient := f.session(t, "audit", "alice")
	target := f.accounts["bob"].ID
	readPath := "/accounts/" + target
	initial := f.state(t)
	if !initial.enabled || initial.revision != identityAuditRemoteRevision || initial.sessions != 1 || initial.records != 1 {
		t.Fatal("unexpected isolated account/session/Audit setup", initial)
	}
	f.requireSession(t, true)
	response := f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodGet, readPath, nil)
	if response.status != http.StatusOK {
		t.Fatal("HTTP exact-account read failed", response.status)
	}
	account := identityAuditRemoteDecode[identityremote.AccountInfo](t, response.data)
	if account.ID != target || account.Login != "remote-bob" || !account.Enabled || account.Revision != "9007199254740993" {
		t.Fatal("exact Account representation lost its string revision", account)
	}
	mcpAccount := identityAuditRemoteSuccess[identityremote.AccountInfo](t, bob, identityremote.AccountOperation, identityremote.AccountInput{AccountID: target})
	direct, err := f.identity.Account(context.Background(), achrix.Principal(f.accounts["alice"].ID), target)
	if err != nil || direct.Revision != identityAuditRemoteRevision || !reflect.DeepEqual(account, mcpAccount) {
		t.Fatal("direct/HTTP/MCP Account views differ", err)
	}
	wrongAudience := f.request(t, "identity-http", f.tokens["identity-mcp/alice"], http.MethodGet, readPath, nil)
	if fault := identityAuditRemoteHTTPFault(t, wrongAudience); wrongAudience.status != http.StatusUnauthorized || fault.Code != "invalid_credential" {
		t.Fatal("MCP credential crossed the HTTP audience", fault)
	}
	wrongComponent := f.request(t, "identity-http", f.tokens["audit-http/alice"], http.MethodGet, readPath, nil)
	if fault := identityAuditRemoteHTTPFault(t, wrongComponent); wrongComponent.status != http.StatusUnauthorized || fault.Code != "invalid_credential" {
		t.Fatal("Audit credential crossed the Identity resource", fault)
	}
	input := identityremote.SetEnabledInput{AccountID: target, ExpectedRevision: account.Revision, Enabled: false}
	identityAuditRemoteExpectFault(t, bob, identityremote.SetEnabledOperation, input, "denied")
	identityAuditRemoteExpectFault(t, bob, identityremote.AccountOperation, identityremote.AccountInput{AccountID: f.accounts["alice"].ID}, "denied")
	before := f.countCalls(identity.AccountRead)
	identityAuditRemoteExpectFault(t, bob, identityremote.AccountOperation, map[string]any{"account_id": target, "actor": f.accounts["alice"].ID, "client_id": "FORGED_IDENTITY_AUDIT_CLIENT"}, "invalid_arguments")
	if f.countCalls(identity.AccountRead) != before {
		t.Fatal("claimed identity reached Account policy")
	}
	badHTTP := f.request(t, "identity-http", f.tokens["identity-http/bob"], http.MethodPost, readPath+"/enabled", map[string]any{"expected_revision": account.Revision, "enabled": false, "actor": f.accounts["alice"].ID})
	if fault := identityAuditRemoteHTTPFault(t, badHTTP); fault.Code != "invalid_arguments" || fault.Outcome != bridge.NotExecuted {
		t.Fatal("HTTP body identity claim was admitted", fault)
	}
	forgedHeaders := f.request(t, "identity-http", f.tokens["identity-http/bob"], http.MethodPost, readPath+"/enabled", map[string]any{"expected_revision": account.Revision, "enabled": false})
	if fault := identityAuditRemoteHTTPFault(t, forgedHeaders); fault.Code != "denied" || fault.Outcome != bridge.NotExecuted {
		t.Fatal("forged HTTP actor/client headers granted management authority", fault)
	}
	identityAuditRemoteExpectFault(t, alice, identityremote.SetEnabledOperation, identityremote.SetEnabledInput{AccountID: target, ExpectedRevision: "9007199254740992", Enabled: false}, "conflict")
	invalid := identityAuditRemoteExpectFault(t, alice, identityremote.SetEnabledOperation, map[string]any{"account_id": target, "expected_revision": json.Number("9007199254740993"), "enabled": false}, "invalid_arguments")
	if invalid.Outcome != bridge.NotExecuted || len(invalid.Fields) == 0 {
		t.Fatal("numeric revision lost safe input guidance", invalid)
	}
	identityAuditRemoteExpectFault(t, alice, identityremote.SetEnabledOperation, map[string]any{"account_id": target, "expected_revision": account.Revision}, "invalid_arguments")
	f.grant("alice", audit.Append, target, false)
	denied := identityAuditRemoteExpectFault(t, alice, identityremote.SetEnabledOperation, input, "denied")
	if denied.Outcome != bridge.NotExecuted || denied.Reference != "" {
		t.Fatal("denied Audit append invented an executed mutation", denied)
	}
	f.grant("alice", audit.Append, target, true)
	if got := f.state(t); got != initial {
		t.Fatal("denied/invalid/stale operation changed account, session or Audit", got)
	}
	f.requireSession(t, true)
	f.mu.Lock()
	f.blocked["identity-mcp/"+identityremote.SetEnabledOperation] = true
	f.mu.Unlock()
	before = f.countCalls(identity.AccountSetEnabled)
	identityAuditRemoteExpectFault(t, alice, identityremote.SetEnabledOperation, input, "denied")
	if f.countCalls(identity.AccountSetEnabled) != before {
		t.Fatal("channel restriction reached mutation policy")
	}
	f.mu.Lock()
	f.blocked["identity-mcp/"+identityremote.SetEnabledOperation] = false
	f.enabled["identity-http"] = false
	f.mu.Unlock()
	before = f.countCalls(identity.AccountRead)
	if fault := identityAuditRemoteHTTPFault(t, f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodGet, readPath, nil)); fault.Code != "denied" || fault.Outcome != bridge.NotExecuted || f.countCalls(identity.AccountRead) != before {
		t.Fatal("connection disablement reached Account policy", fault)
	}
	identityAuditRemoteSuccess[identityremote.AccountInfo](t, alice, identityremote.AccountOperation, identityremote.AccountInput{AccountID: target})
	f.mu.Lock()
	f.enabled["identity-http"] = true
	f.mu.Unlock()

	// This actual Audit write fails after transaction-local account/session
	// changes. The public coarse unavailable category cannot promise rollback;
	// independently inspect persisted state and retain the protected cause.
	f.exec(t, "CREATE FUNCTION audit.remote_reject_append() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_IDENTITY_AUDIT_APPEND_CAUSE'; END $$; CREATE TRIGGER remote_reject_append BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.remote_reject_append()")
	before = f.countCalls(identity.AccountSetEnabled)
	failedResponse := f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodPost, readPath+"/enabled", map[string]any{"expected_revision": account.Revision, "enabled": false})
	failure := identityAuditRemoteHTTPFault(t, failedResponse)
	if failure.Code != "identity_unavailable" || failure.Outcome != bridge.Unknown || f.countCalls(identity.AccountSetEnabled) != before+1 {
		t.Fatal("actual append failure lost its unknown outward outcome or was replayed", failure)
	}
	if got := f.state(t); got != initial {
		t.Fatal("Audit append failure committed a partial Identity mutation", got)
	}
	f.requireSession(t, true)
	unchanged := identityAuditRemoteSuccess[identityremote.AccountInfo](t, alice, identityremote.AccountOperation, identityremote.AccountInput{AccountID: target})
	history := identityAuditRemoteSuccess[auditremote.Page](t, auditClient, auditremote.QueryOperation, auditremote.PageInput{Target: target})
	if !unchanged.Enabled || unchanged.Revision != input.ExpectedRevision || len(history.Records) != initial.records {
		t.Fatal("authorized Account/Audit reconciliation disagrees with independently verified rollback")
	}
	f.mu.Lock()
	protected, terminal := false, false
	for _, cause := range f.causes {
		var databaseCause interface{ SQLState() string }
		if cause.owner == "audit" && cause.operation == "append" && cause.phase == "execute" && cause.request.ID == failure.RequestID && cause.request.Operation == identityremote.SetEnabledOperation && cause.request.Channel == "http" && string(cause.request.Caller.Principal) == f.accounts["alice"].ID && cause.request.Caller.ClientID == "notes-identity-http" && cause.request.Caller.ConnectionID == "identity-http-"+f.accounts["alice"].ID && errors.As(cause.cause, &databaseCause) && databaseCause.SQLState() == "P0001" && strings.Contains(cause.cause.Error(), "PRIVATE_IDENTITY_AUDIT_APPEND_CAUSE") {
			protected = true
		}
	}
	for _, event := range f.events {
		if event.Request.ID == failure.RequestID && event.Request.Operation == identityremote.SetEnabledOperation && event.Dispatched && event.Outcome == bridge.Unknown && errors.Is(event.Cause, identity.ErrUnavailable) {
			terminal = true
		}
	}
	f.mu.Unlock()
	if !protected || !terminal {
		t.Fatal("append failure lost private cause or trusted Bridge outcome", protected, terminal)
	}
	f.assertSafe(t, failedResponse.data)
	f.exec(t, "DROP TRIGGER remote_reject_append ON audit.records; DROP FUNCTION audit.remote_reject_append()")
	before = f.countCalls(identity.AccountSetEnabled)
	changed := identityAuditRemoteSuccess[identityremote.SetEnabledResult](t, alice, identityremote.SetEnabledOperation, input)
	if changed.AccountID != target || changed.Outcome != "succeeded" || f.countCalls(identity.AccountSetEnabled) != before+1 {
		t.Fatal("confirmed MCP status change lost its identity or was replayed", changed)
	}
	if got := f.state(t); got.enabled || got.revision != identityAuditRemoteRevision+1 || got.sessions != 0 || got.records != initial.records+1 {
		t.Fatal("status, session revocation and one Audit event were not atomic", got)
	}
	f.requireSession(t, false)
	account = identityAuditRemoteSuccess[identityremote.AccountInfo](t, alice, identityremote.AccountOperation, identityremote.AccountInput{AccountID: target})
	if account.Enabled || account.Revision != "9007199254740994" {
		t.Fatal("authorized Account failed to reconcile the confirmed status change", account)
	}
	revoked := f.request(t, "identity-http", f.tokens["identity-http/bob"], http.MethodGet, readPath, nil)
	if fault := identityAuditRemoteHTTPFault(t, revoked); revoked.status != http.StatusUnauthorized || fault.Code != "invalid_credential" {
		t.Fatal("resource verifier retained a revoked Identity session", fault)
	}
	page := identityAuditRemoteSuccess[auditremote.Page](t, auditClient, auditremote.QueryOperation, auditremote.PageInput{Target: target})
	if len(page.Records) != 2 || page.Records[0].Attribution != nil {
		t.Fatal("direct bootstrap invented attribution or mutation duplicated Audit", page)
	}
	f.checkAttributedRecord(t, page, "mcp", "")
	// A second explicit management decision covers successful HTTP JSON wiring;
	// re-enabling never resurrects the old session revoked by the earlier change.
	f.grant("alice", identity.AccountRead, target, false)
	beforeRead := f.countCalls(identity.AccountRead)
	before = f.countCalls(identity.AccountSetEnabled)
	enabled := f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodPost, readPath+"/enabled", map[string]any{"expected_revision": account.Revision, "enabled": true})
	if enabled.status != http.StatusOK {
		t.Fatal("HTTP conditional status change failed", enabled.status)
	}
	changed = identityAuditRemoteDecode[identityremote.SetEnabledResult](t, enabled.data)
	if changed.AccountID != target || changed.Outcome != "succeeded" || f.countCalls(identity.AccountSetEnabled) != before+1 || f.countCalls(identity.AccountRead) != beforeRead {
		t.Fatal("HTTP status change lost its confirmed identity, replayed or added an unauthorized implicit Account read", changed)
	}
	if got := f.state(t); !got.enabled || got.revision != identityAuditRemoteRevision+2 || got.sessions != 0 || got.records != initial.records+2 {
		t.Fatal("HTTP status change and Audit count differ", got)
	}
	f.requireSession(t, false)
	identityAuditRemoteExpectFault(t, alice, identityremote.AccountOperation, identityremote.AccountInput{AccountID: target}, "denied")
	f.grant("alice", identity.AccountRead, target, true)
	response = f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodGet, readPath, nil)
	if response.status != http.StatusOK {
		t.Fatal("HTTP Account read after mutation failed", response.status)
	}
	account = identityAuditRemoteDecode[identityremote.AccountInfo](t, response.data)
	if !account.Enabled || account.Revision != "9007199254740995" {
		t.Fatal("HTTP Account lost the next exact revision", account)
	}
	page = identityAuditRemoteSuccess[auditremote.Page](t, auditClient, auditremote.ExportOperation, auditremote.PageInput{Target: target})
	f.checkAttributedRecord(t, page, "http", enabled.header.Get("X-Request-ID"))
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	f.assertSafe(t, encoded)
	privateCause := errors.New("PRIVATE_IDENTITY_AUDIT_POLICY_CAUSE")
	f.mu.Lock()
	f.policyFailureCapability = identity.AccountRead
	f.policyCause = privateCause
	f.mu.Unlock()
	policyResponse := f.request(t, "identity-http", f.tokens["identity-http/alice"], http.MethodGet, readPath, nil)
	policyFailure := identityAuditRemoteHTTPFault(t, policyResponse)
	if policyFailure.Code != "authorization_unavailable" || policyFailure.Outcome != bridge.NotExecuted {
		t.Fatal("policy outage became denial or query effects", policyFailure)
	}
	f.mu.Lock()
	retained := false
	for _, cause := range f.causes {
		if cause.owner == "core" && cause.operation == "authorize" && cause.phase == "policy" && cause.request.ID == policyFailure.RequestID && cause.request.Operation == identityremote.AccountOperation && cause.cause == privateCause && string(cause.request.Caller.Principal) == f.accounts["alice"].ID {
			retained = true
		}
	}
	f.mu.Unlock()
	if !retained {
		t.Fatal("Core lost original policy cause and trusted request correlation")
	}
	f.assertSafe(t, policyResponse.data)
}

func TestPublicIdentityAuditRemotePagesAndCurrentAuthority(t *testing.T) {
	f := newIdentityAuditRemoteFixture(t)
	alice, bob := f.session(t, "audit", "alice"), f.session(t, "audit", "bob")
	target := f.accounts["bob"].ID
	appendEvent := func(action string) audit.Record {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		record, err := f.audit.Append(ctx, achrix.Principal(f.accounts["alice"].ID), audit.Event{Action: action, Target: target, Authority: audit.Append, Outcome: "succeeded"})
		if err != nil {
			t.Fatal("trusted fixture Audit setup:", err)
		}
		return record
	}
	for i := 0; i < 21; i++ {
		appendEvent("fixture.remote.seed")
	}
	query := "/records?" + url.Values{"target": {target}}.Encode()
	export := "/export?" + url.Values{"target": {target}}.Encode()
	response := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, query, nil)
	if response.status != http.StatusOK {
		t.Fatal("HTTP Audit query failed", response.status)
	}
	first := identityAuditRemoteDecode[auditremote.Page](t, response.data)
	if len(first.Records) != 20 || first.NextCursor == "" {
		t.Fatal("omitted Audit page limit did not select bounded20", first)
	}
	for _, record := range first.Records {
		if record.Target != target || record.Attribution != nil {
			t.Fatal("query leaked another target or fabricated direct attribution", record)
		}
	}
	exported := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, export, nil)
	if exported.status != http.StatusOK || !reflect.DeepEqual(first, identityAuditRemoteDecode[auditremote.Page](t, exported.data)) {
		t.Fatal("HTTP Query and separately permitted Export returned different retained pages")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	direct, err := f.audit.Query(ctx, achrix.Principal(f.accounts["alice"].ID), target, "", 20)
	cancel()
	if err != nil || len(direct.Records) != len(first.Records) || direct.NextCursor != first.NextCursor {
		t.Fatal("direct and remote Audit page contracts differ", err)
	}
	for i, record := range direct.Records {
		if record.ID != first.Records[i].ID {
			t.Fatal("remote query reordered retained records")
		}
	}
	limit := 2
	bobFirst := identityAuditRemoteSuccess[auditremote.Page](t, bob, auditremote.QueryOperation, auditremote.PageInput{Target: target, Limit: &limit})
	if len(bobFirst.Records) != 2 || bobFirst.NextCursor == "" {
		t.Fatal("MCP bounded page failed", bobFirst)
	}
	identityAuditRemoteExpectFault(t, bob, auditremote.ExportOperation, auditremote.PageInput{Target: target, Limit: &limit}, "denied")
	f.grant("bob", audit.Export, target, true)
	bobExport := identityAuditRemoteSuccess[auditremote.Page](t, bob, auditremote.ExportOperation, auditremote.PageInput{Target: target, Limit: &limit})
	if !reflect.DeepEqual(bobFirst, bobExport) {
		t.Fatal("explicit MCP Export grant changed the retained page")
	}
	f.grant("bob", audit.Query, target, false)
	continuation := auditremote.PageInput{Target: target, Cursor: bobFirst.NextCursor, Limit: &limit}
	if fault := identityAuditRemoteExpectFault(t, bob, auditremote.QueryOperation, continuation, "denied"); fault.Outcome != bridge.NotExecuted {
		t.Fatal("Query revocation became state-changing", fault)
	}
	identityAuditRemoteSuccess[auditremote.Page](t, bob, auditremote.ExportOperation, continuation)
	f.grant("bob", audit.Query, target, true)
	f.grant("bob", audit.Export, target, false)
	identityAuditRemoteExpectFault(t, bob, auditremote.ExportOperation, continuation, "denied")
	identityAuditRemoteSuccess[auditremote.Page](t, bob, auditremote.QueryOperation, continuation)
	identityAuditRemoteExpectFault(t, bob, auditremote.QueryOperation, auditremote.PageInput{Target: f.accounts["alice"].ID}, "denied")
	f.grant("alice", audit.Query, f.accounts["alice"].ID, true)
	wrongTarget := identityAuditRemoteExpectFault(t, alice, auditremote.QueryOperation, auditremote.PageInput{Target: f.accounts["alice"].ID, Cursor: first.NextCursor}, "invalid_arguments")
	if wrongTarget.Outcome != bridge.NotExecuted || len(wrongTarget.Fields) == 0 {
		t.Fatal("cross-target cursor lost input guidance", wrongTarget)
	}
	before := f.countCalls(audit.Query)
	identityAuditRemoteExpectFault(t, bob, auditremote.QueryOperation, map[string]any{"target": target, "actor": f.accounts["alice"].ID, "client_id": "FORGED_IDENTITY_AUDIT_CLIENT"}, "invalid_arguments")
	forgedQuery := f.request(t, "audit-http", f.tokens["audit-http/bob"], http.MethodGet, query+"&actor="+f.accounts["alice"].ID, nil)
	if fault := identityAuditRemoteHTTPFault(t, forgedQuery); fault.Code != "invalid_arguments" || f.countCalls(audit.Query) != before {
		t.Fatal("claimed Audit actor reached ordinary policy", fault)
	}
	zero := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, query+"&limit=0", nil)
	if fault := identityAuditRemoteHTTPFault(t, zero); fault.Code != "invalid_arguments" || fault.Outcome != bridge.NotExecuted || len(fault.Fields) == 0 {
		t.Fatal("explicit zero page limit became the default", fault)
	}
	tooMany := 101
	identityAuditRemoteExpectFault(t, alice, auditremote.ExportOperation, auditremote.PageInput{Target: target, Limit: &tooMany}, "invalid_arguments")
	maximum := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, export+"&limit=100", nil)
	if maximum.status != http.StatusOK {
		t.Fatal("maximum bounded Export page rejected", maximum.status)
	}
	all := identityAuditRemoteDecode[auditremote.Page](t, maximum.data)
	if len(all.Records) != 22 || all.NextCursor != "" {
		t.Fatal("maximum Export page did not retain all exact-target records", all)
	}
	f.grant("alice", audit.Query, "authorized-empty-target", true)
	empty := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, "/records?target=authorized-empty-target", nil)
	if empty.status != http.StatusOK {
		t.Fatal("authorized empty Audit target became existence error", empty.status)
	}
	emptyPage := identityAuditRemoteDecode[auditremote.Page](t, empty.data)
	if emptyPage.Records == nil || len(emptyPage.Records) != 0 || emptyPage.NextCursor != "" {
		t.Fatal("empty Audit result is not an explicit array", emptyPage)
	}
	f.mu.Lock()
	f.blocked["audit-mcp/"+auditremote.QueryOperation] = true
	f.mu.Unlock()
	before = f.countCalls(audit.Query)
	identityAuditRemoteExpectFault(t, alice, auditremote.QueryOperation, auditremote.PageInput{Target: target}, "denied")
	if f.countCalls(audit.Query) != before {
		t.Fatal("Audit channel gate reached product Query policy")
	}
	if result := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, query, nil); result.status != http.StatusOK {
		t.Fatal("MCP gate altered HTTP Query authority")
	}
	f.mu.Lock()
	f.blocked["audit-mcp/"+auditremote.QueryOperation] = false
	f.enabled["audit-http"] = false
	f.mu.Unlock()
	before = f.countCalls(audit.Query)
	if fault := identityAuditRemoteHTTPFault(t, f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, query, nil)); fault.Code != "denied" || fault.Outcome != bridge.NotExecuted || f.countCalls(audit.Query) != before {
		t.Fatal("disabled Audit connection reached product policy", fault)
	}
	identityAuditRemoteSuccess[auditremote.Page](t, alice, auditremote.ExportOperation, auditremote.PageInput{Target: target})
	f.mu.Lock()
	f.enabled["audit-http"] = true
	f.mu.Unlock()

	// A later committed record can appear after an earlier cursor. The cursor
	// scopes one target and traversal position, not a frozen export snapshot.
	later := appendEvent("fixture.remote.later")
	values := url.Values{"target": {target}, "cursor": {first.NextCursor}}
	tailResponse := f.request(t, "audit-http", f.tokens["audit-http/alice"], http.MethodGet, "/records?"+values.Encode(), nil)
	if tailResponse.status != http.StatusOK {
		t.Fatal("HTTP current continuation failed", tailResponse.status)
	}
	tail := identityAuditRemoteDecode[auditremote.Page](t, tailResponse.data)
	if len(tail.Records) != 3 || tail.NextCursor != "" || tail.Records[len(tail.Records)-1].ID != later.ID {
		t.Fatal("Audit continuation invented snapshot semantics or lost a committed later record", tail)
	}
	mcpTail := identityAuditRemoteSuccess[auditremote.Page](t, alice, auditremote.QueryOperation, auditremote.PageInput{Target: target, Cursor: first.NextCursor})
	mcpExport := identityAuditRemoteSuccess[auditremote.Page](t, alice, auditremote.ExportOperation, auditremote.PageInput{Target: target, Cursor: first.NextCursor})
	if !reflect.DeepEqual(tail, mcpTail) || !reflect.DeepEqual(tail, mcpExport) {
		t.Fatal("HTTP/MCP Query/Export continuation semantics differ")
	}
	f.assertSafe(t, response.data)
	f.assertSafe(t, exported.data)
	f.assertSafe(t, tailResponse.data)
}
