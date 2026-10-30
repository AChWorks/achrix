// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
	mediaremote "github.com/AChWorks/achrix/media/remote"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mediaRemoteCause struct {
	owner, operation, phase string
	request                 bridge.Request
	cause                   error
}

type mediaRemotePolicyCall struct {
	capability, target string
	request            bridge.Request
}

// Credentials and mutable grants are product-fixture state. The real Media
// Service always invokes this one Core policy; adapters do not copy its rights.
type mediaRemoteFixture struct {
	service         *media.Service
	app             *achrix.Application
	admin, pool     *pgxpool.Pool
	database, root  string
	server          *httptest.Server
	client          *http.Client
	httpURL, mcpURL string
	tokens          map[string]string
	credentials     map[string]remoteConsumerCredential
	mu              sync.Mutex
	grants          map[achrix.Principal]map[string]bool
	enabled         map[string]bool
	blocked         map[string]bool
	policyCause     error
	policyCalls     []mediaRemotePolicyCall
	causes          []mediaRemoteCause
	events          []bridge.Diagnostic
	logs            safeBuffer
}

func newMediaRemoteFixture(t *testing.T) *mediaRemoteFixture {
	t.Helper()
	f := &mediaRemoteFixture{
		tokens: map[string]string{}, credentials: map[string]remoteConsumerCredential{},
		grants:  map[achrix.Principal]map[string]bool{"alice": {}, "bob": {}},
		enabled: map[string]bool{"http": true, "mcp": true}, blocked: map[string]bool{},
	}
	// This is the explicitly provisioned local validation cluster. A separate
	// database/root preserves the five-asset Media capture/restore fixture.
	dsn := os.Getenv("NOTES_MEDIA_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOTES_MEDIA_DATABASE_URL is required; full validate.sh executes this real PostgreSQL proof")
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil || base.ConnConfig.Database != "achrix_media_consumer" ||
		!filepath.IsAbs(base.ConnConfig.Host) || filepath.Base(base.ConnConfig.Host) != "socket" ||
		!strings.HasPrefix(filepath.Dir(base.ConnConfig.Host), "/tmp/achrix-validation.") || base.ConnConfig.TLSConfig != nil {
		t.Fatal("the controlled local NOTES_MEDIA_DATABASE_URL validation cluster is required")
	}
	clusterRoot := filepath.Dir(base.ConnConfig.Host)
	info, err := os.Lstat(clusterRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("Media remote proof requires the private task-owned validation cluster")
	}
	for _, fallback := range base.ConnConfig.Fallbacks {
		if fallback == nil || fallback.Host != base.ConnConfig.Host || fallback.Port != base.ConnConfig.Port || fallback.TLSConfig != nil {
			t.Fatal("Media remote proof cannot create databases through another fallback host")
		}
	}
	base.MaxConns = 1
	base.ConnConfig.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.admin, err = pgxpool.NewWithConfig(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.close(t) })
	var actual, encoding string
	var version int
	if err := f.admin.QueryRow(ctx, "SELECT current_database(),current_setting('server_version_num')::integer,current_setting('server_encoding')").Scan(&actual, &version, &encoding); err != nil || actual != "achrix_media_consumer" || version != 180006 || encoding != "UTF8" {
		t.Fatal("Media remote validation cluster identity/profile mismatch", err)
	}
	name := "achrix_media_remote_" + strings.ToLower(rand.Text())
	if _, err := f.admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		t.Fatal("create owned Media remote database:", err)
	}
	f.database = name // Only a successfully created exact name is cleanup-owned.
	connection := &url.URL{Scheme: "postgresql", Host: "localhost", Path: "/" + name, User: url.UserPassword(base.ConnConfig.User, base.ConnConfig.Password)}
	connection.RawQuery = url.Values{
		"host": {base.ConnConfig.Host}, "port": {strconv.Itoa(int(base.ConnConfig.Port))}, "sslmode": {"disable"},
	}.Encode()
	privateDSN := connection.String()
	f.root, err = os.MkdirTemp(clusterRoot, "media-remote.")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := media.Migrate(ctx, privateDSN); err != nil {
		t.Fatal("migrate owned Media database:", err)
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
			f.causes = append(f.causes, mediaRemoteCause{owner, operation, phase, request, cause})
			f.mu.Unlock()
		}
	}
	module, err := media.NewPostgres(privateDSN, media.Config{
		StorageRoot: f.root, AllowedMIMEs: []string{"text/plain"}, MaxConns: 4, MaxOperations: 4, Diagnostic: capture("media"),
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(ctx context.Context, actor achrix.Principal, capability, target string) error {
		request, _ := bridge.RequestFromContext(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.policyCalls = append(f.policyCalls, mediaRemotePolicyCall{capability, target, request})
		if f.policyCause != nil {
			return f.policyCause
		}
		if f.grants[actor][capability+"\x00"+target] {
			return nil
		}
		return achrix.ErrDenied
	})
	f.app, err = achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second, Logger: logger, Diagnostic: capture("core")}, policy, module)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = media.NewService(f.app, module)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	f.grant("alice", media.Create, media.LibraryTarget, true)
	f.grant("alice", media.List, media.LibraryTarget, true)
	f.grant("bob", media.List, media.LibraryTarget, true)
	mux := http.NewServeMux()
	f.server = httptest.NewTLSServer(mux)
	f.client = f.server.Client()
	f.client.Timeout = 20 * time.Second
	f.httpURL, f.mcpURL = f.server.URL+"/api/media/v1", f.server.URL+"/mcp/media"
	for _, channel := range []string{"http", "mcp"} {
		audience := f.httpURL
		if channel == "mcp" {
			audience = f.mcpURL
		}
		for _, actor := range []achrix.Principal{"alice", "bob"} {
			token := rand.Text() + rand.Text()
			f.tokens[channel+"/"+string(actor)] = token
			f.credentials[token] = remoteConsumerCredential{actor, audience}
		}
	}
	for _, channel := range []string{"http", "mcp"} {
		resource := f.httpURL
		if channel == "mcp" {
			resource = f.mcpURL
		}
		cfg := bridge.Config{
			ResourceURL: resource, AuthorizationServers: []string{"https://identity.example.test"}, Scopes: []string{"media"},
			Verify: func(_ context.Context, token string) (bridge.Credential, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				credential, exists := f.credentials[token]
				if !exists || credential.audience != resource {
					return bridge.Credential{}, bridge.ErrInvalidCredential
				}
				return bridge.Credential{Caller: bridge.Caller{Principal: credential.principal, ClientID: "notes-media-" + channel, ConnectionID: channel + "-" + string(credential.principal)}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"media"}}, nil
			},
			Admit: func(context.Context, bridge.Caller) error {
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
			// Deliberately retain the constructors' published byte-limit defaults:
			// the >1MiB transfer must not need an undocumented test-only override.
			Limits: bridge.Limits{Concurrent: 4, Timeout: 16 * time.Second},
		}
		var endpoint *bridge.Endpoint
		if channel == "http" {
			endpoint, err = mediaremote.NewHTTP(f.service, cfg)
			if err == nil {
				mux.Handle("/api/media/v1/", endpoint)
			}
		} else {
			endpoint, err = mediaremote.NewMCP(f.service, cfg, f.httpURL)
			if err == nil {
				mux.Handle("/mcp/media", endpoint)
			}
		}
		if err != nil {
			t.Fatal("compose public Media remote adapter:", err)
		}
	}
	return f
}

func (f *mediaRemoteFixture) close(t *testing.T) {
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
		t.Errorf("Media remote shutdown unverified; retaining owned database %s and root %s: %v", f.database, f.root, shutdown)
		return
	}
	if f.database != "" {
		if _, err := f.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{f.database}.Sanitize()); err != nil {
			t.Errorf("Media remote cleanup failed; retaining owned database %s/root %s: %v", f.database, f.root, err)
			return
		}
	}
	if f.root != "" {
		if err := os.RemoveAll(f.root); err != nil {
			t.Error("remove owned Media root:", err)
		}
	}
}

func (f *mediaRemoteFixture) grant(actor achrix.Principal, capability, target string, allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[actor][capability+"\x00"+target] = allowed
}

func (f *mediaRemoteFixture) countCalls(capability string) int {
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

type mediaRemoteFault struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	Reference string            `json:"reference"`
	RequestID string            `json:"request_id"`
}

type mediaRemoteResponse struct {
	status int
	header http.Header
	data   []byte
}

func (f *mediaRemoteFixture) request(t *testing.T, token, method, path, contentType string, body io.Reader, disposition string) mediaRemoteResponse {
	t.Helper()
	request, err := http.NewRequest(method, f.httpURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Actor", "alice")
	request.Header.Set("X-Client-ID", "FORGED_MEDIA_CLIENT")
	request.Header.Set("X-Request-ID", "FORGED_MEDIA_REQUEST")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if disposition != "" {
		request.Header.Set("Content-Disposition", disposition)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal("actual HTTPS Media request:", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, media.MaxUploadBytes+4097))
	if err != nil || len(data) > int(media.MaxUploadBytes)+4096 {
		t.Fatal("bounded Media response:", err)
	}
	if id := response.Header.Get("X-Request-ID"); id == "" || id == "FORGED_MEDIA_REQUEST" {
		t.Fatal("Media response lost trusted request correlation")
	}
	return mediaRemoteResponse{response.StatusCode, response.Header.Clone(), data}
}

func mediaRemoteHTTPFault(t *testing.T, response mediaRemoteResponse) mediaRemoteFault {
	t.Helper()
	var fault mediaRemoteFault
	if response.status < 400 || json.Unmarshal(response.data, &fault) != nil || fault.Code == "" || fault.Message == "" || fault.Action == "" || fault.RequestID != response.header.Get("X-Request-ID") {
		t.Fatal("missing useful correlated Media problem", response.status, string(response.data))
	}
	return fault
}

func mediaRemoteDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal("public Media JSON:", err, string(data))
	}
	return value
}

func (f *mediaRemoteFixture) upload(t *testing.T, filename string, data []byte) mediaRemoteResponse {
	t.Helper()
	return f.request(t, f.tokens["http/alice"], http.MethodPost, "/assets", "application/octet-stream", bytes.NewReader(data), mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
}

func (f *mediaRemoteFixture) session(t *testing.T, actor string) *mcp.ClientSession {
	t.Helper()
	transport := &remoteConsumerSDKTransport{base: f.client.Transport, token: f.tokens["mcp/"+actor]}
	httpClient := *f.client
	httpClient.Transport = transport
	client := mcp.NewClient(&mcp.Implementation{Name: "notes-media-consumer", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.mcpURL, HTTPClient: &httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal("official Media SDK connection:", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if transport.getRequests.Load() != 0 {
			t.Error("Media SDK opened an idle SSE stream")
		}
	})
	return session
}

func mediaRemoteCall(t *testing.T, session *mcp.ClientSession, operation string, arguments any) ([]byte, *mediaRemoteFault) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: operation, Arguments: arguments})
	if err != nil {
		t.Fatal("official Media SDK operation:", err)
	}
	if result.IsError {
		if len(result.Content) != 1 {
			t.Fatal("missing safe Media tool failure")
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("Media tool failure is not structured safe text")
		}
		fault := mediaRemoteDecode[mediaRemoteFault](t, []byte(text.Text))
		if fault.Code == "" || fault.Message == "" || fault.Action == "" || fault.RequestID == "" {
			t.Fatal("Media tool failure lost useful evidence", fault)
		}
		return []byte(text.Text), &fault
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, nil
}

func mediaRemoteSuccess[T any](t *testing.T, session *mcp.ClientSession, operation string, arguments any) T {
	t.Helper()
	data, fault := mediaRemoteCall(t, session, operation, arguments)
	if fault != nil {
		t.Fatal("unexpected Media operation failure", fault)
	}
	return mediaRemoteDecode[T](t, data)
}

func mediaRemoteExpectFault(t *testing.T, session *mcp.ClientSession, operation string, arguments any, code string) mediaRemoteFault {
	t.Helper()
	_, fault := mediaRemoteCall(t, session, operation, arguments)
	if fault == nil || fault.Code != code {
		t.Fatal("unexpected Media failure category", code, fault)
	}
	return *fault
}

func TestPublicMediaRemotePrivateWorkflowAndCurrentAuthority(t *testing.T) {
	f := newMediaRemoteFixture(t)
	alice, bob := f.session(t, "alice"), f.session(t, "bob")
	// Compare the transport's safe field projection with the actual owning
	// Service error, including its source-owned reason and pre-intent outcome.
	invalid := mediaRemoteHTTPFault(t, f.upload(t, "../private.txt", []byte("PRIVATE_INVALID_FILENAME_BODY\n")))
	_, directInput := f.service.Create(context.Background(), "alice", "../private.txt", bytes.NewReader([]byte("unused")))
	var input *media.InputError
	if !errors.As(directInput, &input) || invalid.Code != "invalid_arguments" || invalid.Outcome != bridge.NotExecuted || invalid.Reference != "" || invalid.Fields[input.Field()] == "" || invalid.Fields["reason"] != input.Reason() {
		t.Fatal("owning input failure lost safe field/reason/pre-intent evidence", invalid)
	}
	filename := "گزارش\u200cنمونه.txt"
	body := bytes.Repeat([]byte("PRIVATE_MEDIA_REMOTE_BODY ordinary UTF-8 text\n"), 32768)
	if len(body) <= 1<<20 {
		t.Fatal("stream proof must exceed the old JSON response bound")
	}
	upload := f.upload(t, filename, body)
	if upload.status != http.StatusOK {
		t.Fatal("raw private upload:", upload.status, string(upload.data))
	}
	asset := mediaRemoteDecode[mediaremote.AssetInfo](t, upload.data)
	digest := sha256.Sum256(body)
	if asset.ID == "" || asset.State != "ready" || asset.Revision != "2" || asset.Filename != filename || asset.MIME != "text/plain" || asset.Size != int64(len(body)) || asset.SHA256 != hex.EncodeToString(digest[:]) || asset.Content == nil {
		t.Fatal("upload did not preserve byte-derived private metadata", asset)
	}
	when, err := time.Parse(time.RFC3339Nano, asset.CreatedAt)
	if err != nil || when.Location() != time.UTC {
		t.Fatal("public creation time lost UTC identity", asset.CreatedAt, err)
	}
	// Creating an asset and holding its reference grant no collection/object
	// rights beyond the fixture's explicit, independently controlled policy.
	mediaRemoteExpectFault(t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID}, "denied")
	denied := f.request(t, f.tokens["http/alice"], http.MethodGet, "/assets/"+asset.ID+"/content", "", nil, "")
	if fault := mediaRemoteHTTPFault(t, denied); fault.Code != "denied" {
		t.Fatal("uploader acquired byte authority", fault)
	}
	f.grant("alice", media.Read, asset.ID, true)
	status := mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	if status.Content == nil || status.Content.URL != f.httpURL+"/assets/"+asset.ID+"/content" || status.Content.Resource != f.httpURL {
		t.Fatal("reference is not bound to the configured companion resource", status.Content)
	}
	for _, token := range f.tokens {
		if strings.Contains(status.Content.URL, token) {
			t.Fatal("reference contains a credential")
		}
	}
	wrongAudience := f.request(t, f.tokens["mcp/alice"], http.MethodGet, "/assets/"+asset.ID+"/content", "", nil, "")
	if fault := mediaRemoteHTTPFault(t, wrongAudience); wrongAudience.status != http.StatusUnauthorized || fault.Code != "invalid_credential" {
		t.Fatal("MCP credential crossed the HTTP audience", fault)
	}
	denied = f.request(t, f.tokens["http/bob"], http.MethodGet, "/assets/"+asset.ID+"/content", "", nil, "")
	if fault := mediaRemoteHTTPFault(t, denied); fault.Code != "denied" {
		t.Fatal("reference/forged actor header granted Bob bytes", fault)
	}
	f.grant("alice", media.Read, asset.ID, false)
	denied = f.request(t, f.tokens["http/alice"], http.MethodGet, "/assets/"+asset.ID+"/content", "", nil, "")
	if fault := mediaRemoteHTTPFault(t, denied); fault.Code != "denied" {
		t.Fatal("issued reference froze an old read grant", fault)
	}
	f.grant("alice", media.Read, asset.ID, true)
	content := f.request(t, f.tokens["http/alice"], http.MethodGet, "/assets/"+asset.ID+"/content", "", nil, "")
	disposition, parameters, err := mime.ParseMediaType(content.header.Get("Content-Disposition"))
	if content.status != http.StatusOK || !bytes.Equal(content.data, body) || content.header.Get("Content-Type") != "text/plain" || content.header.Get("Content-Length") != strconv.Itoa(len(body)) || content.header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(content.header.Get("Cache-Control"), "private") || !strings.Contains(content.header.Get("Cache-Control"), "no-store") || err != nil || disposition != "attachment" || parameters["filename"] != filename {
		t.Fatal("actual private byte transfer/header contract failed", content.status, content.header, err)
	}
	f.mu.Lock()
	trustedRead := false
	for _, call := range f.policyCalls {
		if call.capability == media.Read && call.target == asset.ID && call.request.ID == content.header.Get("X-Request-ID") && call.request.Operation == mediaremote.ReadOperation && call.request.Channel == "http" && call.request.Caller.Principal == "alice" && call.request.Caller.ClientID == "notes-media-http" && call.request.Caller.ConnectionID == "http-alice" {
			trustedRead = true
		}
	}
	f.mu.Unlock()
	if !trustedRead {
		t.Fatal("successful byte authorization lost trusted identity/channel/correlation")
	}
	secondUpload := f.upload(t, "other.txt", []byte("PRIVATE_SECOND_MEDIA_BODY\n"))
	if secondUpload.status != http.StatusOK {
		t.Fatal("second upload:", secondUpload.status, string(secondUpload.data))
	}
	second := mediaRemoteDecode[mediaremote.AssetInfo](t, secondUpload.data)
	limit := 1
	page := mediaRemoteSuccess[mediaremote.Page](t, bob, mediaremote.ListOperation, mediaremote.ListInput{Limit: &limit})
	if len(page.Assets) != 1 || page.NextCursor == "" || page.Assets[0].Content != nil {
		t.Fatal("whole-library list contract", page)
	}
	f.grant("bob", media.List, media.LibraryTarget, false)
	mediaRemoteExpectFault(t, bob, mediaremote.ListOperation, mediaremote.ListInput{Cursor: page.NextCursor, Limit: &limit}, "denied")
	f.grant("bob", media.List, media.LibraryTarget, true)
	next := mediaRemoteSuccess[mediaremote.Page](t, bob, mediaremote.ListOperation, mediaremote.ListInput{Cursor: page.NextCursor, Limit: &limit})
	if len(next.Assets) != 1 || next.NextCursor != "" || next.Assets[0].Content != nil {
		t.Fatal("whole-library continuation contract", next)
	}
	listed := map[string]bool{page.Assets[0].ID: true, next.Assets[0].ID: true}
	if !listed[asset.ID] || !listed[second.ID] {
		t.Fatal("List was incorrectly filtered by exact Read grants", listed)
	}
	mediaRemoteExpectFault(t, bob, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: second.ID}, "denied")
	before := f.countCalls(media.Read)
	mediaRemoteExpectFault(t, bob, mediaremote.StatusOperation, map[string]any{"asset_id": asset.ID, "actor": "alice", "client_id": "FORGED_MEDIA_CLIENT"}, "invalid_arguments")
	if f.countCalls(media.Read) != before {
		t.Fatal("claimed identity arguments reached the owning policy")
	}
	f.mu.Lock()
	f.blocked["mcp/"+mediaremote.StatusOperation] = true
	f.mu.Unlock()
	mediaRemoteExpectFault(t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID}, "denied")
	if f.countCalls(media.Read) != before {
		t.Fatal("MCP channel restriction reached the owning policy")
	}
	ordinary := f.request(t, f.tokens["http/alice"], http.MethodGet, "/assets/"+asset.ID, "", nil, "")
	if ordinary.status != http.StatusOK {
		t.Fatal("MCP channel gate changed ordinary HTTP authority")
	}
	f.mu.Lock()
	f.blocked["mcp/"+mediaremote.StatusOperation] = false
	f.enabled["http"] = false
	f.mu.Unlock()
	before = f.countCalls(media.Read)
	if fault := mediaRemoteHTTPFault(t, f.request(t, f.tokens["http/alice"], http.MethodGet, "/assets/"+asset.ID, "", nil, "")); fault.Code != "denied" || fault.Outcome != bridge.NotExecuted || f.countCalls(media.Read) != before {
		t.Fatal("administrator disablement failed before dispatch", fault)
	}
	mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	f.mu.Lock()
	f.enabled["http"] = true
	f.mu.Unlock()

	// Establish an isolated stored precondition beyond JavaScript's exact-number
	// range without altering any published schema or the normal restore dataset.
	const largeRevision int64 = 9007199254740993
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx, "UPDATE media.assets SET revision=$2 WHERE id=$1 AND state='ready'", asset.ID, largeRevision); err != nil {
		t.Fatal(err)
	}
	status = mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	direct, err := f.service.Status(ctx, "alice", asset.ID)
	if err != nil || status.Revision != "9007199254740993" || direct.Revision != largeRevision {
		t.Fatal("exact revision changed between public channels", status.Revision, direct.Revision, err)
	}
	f.grant("alice", media.Delete, asset.ID, true)
	mediaRemoteExpectFault(t, alice, mediaremote.DeleteOperation, mediaremote.DeleteInput{AssetID: asset.ID, ExpectedRevision: "9007199254740992"}, "conflict")
	mediaRemoteExpectFault(t, alice, mediaremote.DeleteOperation, map[string]any{"asset_id": asset.ID, "expected_revision": json.Number("9007199254740993")}, "invalid_arguments")
	status = mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	if status.State != "ready" || status.Revision != "9007199254740993" {
		t.Fatal("invalid/stale precondition changed state", status)
	}
	f.grant("alice", media.Read, asset.ID, false)
	deleted := mediaRemoteSuccess[mediaremote.DeleteResult](t, alice, mediaremote.DeleteOperation, mediaremote.DeleteInput{AssetID: asset.ID, ExpectedRevision: status.Revision})
	if deleted.AssetID != asset.ID || deleted.Outcome != "succeeded" {
		t.Fatal("confirmed deletion lost its truthful identity/outcome", deleted)
	}
	f.grant("alice", media.Read, asset.ID, true)
	tombstoneBytes, fault := mediaRemoteCall(t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	if fault != nil {
		t.Fatal(fault)
	}
	tombstone := mediaRemoteDecode[mediaremote.AssetInfo](t, tombstoneBytes)
	if tombstone.ID != asset.ID || tombstone.State != "deleted" || tombstone.Revision != "9007199254740995" || tombstone.Content != nil {
		t.Fatal("minimal tombstone lost exact lifecycle state", tombstone)
	}
	fields := mediaRemoteDecode[map[string]json.RawMessage](t, tombstoneBytes)
	for _, key := range []string{"filename", "mime", "sha256", "size", "width", "height", "content"} {
		if _, exists := fields[key]; exists {
			t.Fatal("deleted metadata was not cleared", key)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, asset.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed deletion retained original bytes", err)
	}

	// The second ready original also exercises the successful HTTP JSON route;
	// the first original above deliberately retains MCP Delete coverage.
	f.grant("alice", media.Delete, second.ID, true)
	deleteBody, err := json.Marshal(map[string]string{"expected_revision": second.Revision})
	if err != nil {
		t.Fatal(err)
	}
	httpDeleted := f.request(t, f.tokens["http/alice"], http.MethodPost, "/assets/"+second.ID+"/delete", "application/json", bytes.NewReader(deleteBody), "")
	if httpDeleted.status != http.StatusOK {
		t.Fatal("HTTP conditional deletion failed", httpDeleted.status, string(httpDeleted.data))
	}
	deleteResult := mediaRemoteDecode[mediaremote.DeleteResult](t, httpDeleted.data)
	if deleteResult.AssetID != second.ID || deleteResult.Outcome != "succeeded" {
		t.Fatal("HTTP deletion lost its confirmed identity/outcome", deleteResult)
	}
	f.grant("alice", media.Read, second.ID, true)
	secondTombstone := mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: second.ID})
	if secondTombstone.ID != second.ID || secondTombstone.State != "deleted" || secondTombstone.Revision != "4" || secondTombstone.Content != nil || secondTombstone.Filename != "" {
		t.Fatal("HTTP deletion did not produce the MCP-visible minimal tombstone", secondTombstone)
	}

	privateCause := errors.New("PRIVATE_MEDIA_AUTHORIZATION_PROVIDER")
	f.mu.Lock()
	f.policyCause = privateCause
	f.mu.Unlock()
	failedBytes, failed := mediaRemoteCall(t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: asset.ID})
	if failed == nil || failed.Code != "authorization_unavailable" || failed.Outcome != bridge.NotExecuted {
		t.Fatal("policy outage became an ordinary denial", failed)
	}
	f.mu.Lock()
	retained := false
	for _, cause := range f.causes {
		if cause.owner == "core" && cause.request.ID == failed.RequestID && cause.operation == "authorize" && cause.phase == "policy" && cause.cause == privateCause && cause.request.Operation == mediaremote.StatusOperation && cause.request.Channel == "mcp" && cause.request.Caller.Principal == "alice" && cause.request.Caller.ClientID == "notes-media-mcp" && cause.request.Caller.ConnectionID == "mcp-alice" {
			retained = true
		}
	}
	f.mu.Unlock()
	if !retained {
		t.Fatal("Core did not retain the original cause with trusted remote attribution")
	}
	for _, secret := range []string{privateCause.Error(), f.root, f.tokens["mcp/alice"], "PRIVATE_MEDIA_REMOTE_BODY", "FORGED_MEDIA_CLIENT", "FORGED_MEDIA_REQUEST"} {
		if strings.Contains(string(failedBytes), secret) || strings.Contains(f.logs.String(), secret) {
			t.Fatal("private body/path/token/provider cause leaked into public/ordinary diagnostics")
		}
	}
}

func TestPublicMediaRemotePublicationLossAndBoundedReconciliation(t *testing.T) {
	f := newMediaRemoteFixture(t)
	alice := f.session(t, "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The trigger terminates publication BEFORE ready commits. This is a real
	// lost database session after durable pending intent/file storage, not a
	// claim that ready committed and only its acknowledgement was lost.
	if _, err := f.pool.Exec(ctx, "CREATE FUNCTION media.remote_lose_ready() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='ready' THEN PERFORM pg_terminate_backend(pg_backend_pid()); END IF; RETURN NEW; END $$; CREATE TRIGGER remote_lose_ready BEFORE UPDATE ON media.assets FOR EACH ROW EXECUTE FUNCTION media.remote_lose_ready()"); err != nil {
		t.Fatal(err)
	}
	before := f.countCalls(media.Create)
	response := f.upload(t, "interrupted.txt", []byte("PRIVATE_MEDIA_PUBLICATION_BODY\n"))
	failure := mediaRemoteHTTPFault(t, response)
	if failure.Code != "outcome_unknown" || failure.Outcome != bridge.Unknown || len(failure.Reference) != 26 || f.countCalls(media.Create) != before+1 {
		t.Fatal("unknown creation lost its identity or was replayed", failure)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM media.assets").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("creation did not retain exactly one durable intent", rows, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, failure.Reference)); err != nil {
		t.Fatal("publication uncertainty blindly removed stored bytes", err)
	}
	f.grant("alice", media.Read, failure.Reference, true)
	status := mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: failure.Reference})
	if status.State != "pending" || status.Revision != "1" || status.Content != nil {
		t.Fatal("authorized status invented ready state/reference", status)
	}
	limit := 1
	mediaRemoteExpectFault(t, alice, mediaremote.ReconcileOperation, mediaremote.ReconcileInput{Limit: &limit}, "denied")
	f.grant("alice", media.Reconcile, media.LibraryTarget, true)
	result := mediaRemoteSuccess[mediaremote.ReconcileResult](t, alice, mediaremote.ReconcileOperation, mediaremote.ReconcileInput{Limit: &limit})
	if result.Processed != 1 || result.Busy != 0 {
		t.Fatal("bounded authorized reconciliation did not finish the durable intent", result)
	}
	beforeHTTPReconcile := f.countCalls(media.Reconcile)
	httpReconciled := f.request(t, f.tokens["http/alice"], http.MethodPost, "/reconcile", "application/json", strings.NewReader(`{"limit":1}`), "")
	if httpReconciled.status != http.StatusOK {
		t.Fatal("HTTP bounded reconciliation failed", httpReconciled.status, string(httpReconciled.data))
	}
	httpResult := mediaRemoteDecode[mediaremote.ReconcileResult](t, httpReconciled.data)
	if httpResult.Processed != 0 || httpResult.Busy != 0 || f.countCalls(media.Reconcile) != beforeHTTPReconcile+1 {
		t.Fatal("HTTP reconciliation did not invoke exactly one bounded no-op", httpResult)
	}
	status = mediaRemoteSuccess[mediaremote.AssetInfo](t, alice, mediaremote.StatusOperation, mediaremote.StatusInput{AssetID: failure.Reference})
	if status.State != "deleted" || status.Content != nil || status.Filename != "" {
		t.Fatal("reconciliation published an interrupted upload or retained identifying metadata", status)
	}
	result = mediaRemoteSuccess[mediaremote.ReconcileResult](t, alice, mediaremote.ReconcileOperation, mediaremote.ReconcileInput{Limit: &limit})
	if result.Processed != 0 || result.Busy != 0 {
		t.Fatal("completed reconciliation changed state again", result)
	}
	for _, invalid := range []int{0, 41} {
		fault := mediaRemoteExpectFault(t, alice, mediaremote.ReconcileOperation, mediaremote.ReconcileInput{Limit: &invalid}, "invalid_arguments")
		if fault.Outcome != bridge.NotExecuted || len(fault.Fields) == 0 {
			t.Fatal("bounded operator input lost safe field guidance", fault)
		}
	}
	before = f.countCalls(media.Reconcile)
	tooLarge := f.request(t, f.tokens["http/alice"], http.MethodPost, "/reconcile", "application/json", strings.NewReader(strings.Repeat(" ", 4096)+`{"limit":1}`), "")
	inputFailure := mediaRemoteHTTPFault(t, tooLarge)
	if tooLarge.status != http.StatusRequestEntityTooLarge || inputFailure.Outcome != bridge.NotExecuted || f.countCalls(media.Reconcile) != before {
		t.Fatal("oversized JSON reached the operator Service", inputFailure)
	}
	f.mu.Lock()
	original, terminal := false, false
	for _, cause := range f.causes {
		var databaseCause interface{ SQLState() string }
		if cause.owner == "media" && cause.operation == "create_publish" && cause.request.ID == failure.RequestID && errors.Is(cause.cause, media.ErrUnknownOutcome) && errors.As(cause.cause, &databaseCause) && databaseCause.SQLState() == "57P01" && cause.request.Caller.Principal == "alice" && cause.request.Caller.ClientID == "notes-media-http" && cause.request.Channel == "http" {
			original = true
		}
	}
	for _, event := range f.events {
		if event.Request.ID == failure.RequestID && event.Request.Operation == mediaremote.CreateOperation && event.Dispatched && event.Outcome == bridge.Unknown && event.Reference == failure.Reference && errors.Is(event.Cause, media.ErrUnknownOutcome) {
			terminal = true
		}
	}
	f.mu.Unlock()
	if !original || !terminal {
		t.Fatal("unknown publication lost protected cause or trusted terminal outcome", original, terminal)
	}
	for _, secret := range []string{f.root, f.tokens["http/alice"], "PRIVATE_MEDIA_PUBLICATION_BODY", "pg_terminate_backend", "FORGED_MEDIA_CLIENT", "FORGED_MEDIA_REQUEST"} {
		if strings.Contains(string(response.data), secret) || strings.Contains(f.logs.String(), secret) {
			t.Fatal("publication diagnostics leaked private bytes/provider context")
		}
	}
	if f.countCalls(media.Create) != 1 {
		t.Fatal("reconciliation replayed the original upload")
	}
}
