// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/infrastructure"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bridgeAcknowledgementLoss preserves the real Store and loses only its
// successful acknowledgement. It does not simulate a PostgreSQL network fault.
type bridgeAcknowledgementLoss struct {
	store  *infrastructure.Store
	cause  error
	mu     sync.Mutex
	writes int
	reads  int
}

func (r *bridgeAcknowledgementLoss) Save(ctx context.Context, note domain.Note) error {
	r.mu.Lock()
	r.writes++
	r.mu.Unlock()
	if err := r.store.Save(ctx, note); err != nil {
		return err
	}
	return r.cause
}
func (r *bridgeAcknowledgementLoss) Find(ctx context.Context, id string) (domain.Note, error) {
	r.mu.Lock()
	r.reads++
	r.mu.Unlock()
	return r.store.Find(ctx, id)
}
func (r *bridgeAcknowledgementLoss) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes, r.reads
}

func TestBridgePostgresAcknowledgementLossReconcilesAuthorizedRead(t *testing.T) {
	// Full validation must provision this separate owned database. Missing
	// prerequisites fail; this proof must not silently skip or alter the two-row
	// dataset used by the existing Notes backup/restore test.
	dsn := os.Getenv("NOTES_BRIDGE_DATABASE_URL")
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || poolConfig.ConnConfig.Database != "achrix_test_bridge" {
		t.Fatal("isolated NOTES_BRIDGE_DATABASE_URL for achrix_test_bridge is required")
	}
	var logs safeBuffer
	logger := diagnosis.New(&logs)
	t.Cleanup(func() { diagnosis.Flush(logger) })
	// The Store constructor validates the local-only fixture DSN before a
	// connection or destructive reset of this exact disposable database.
	store, err := infrastructure.New(dsn, logger)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 4
	poolConfig.ConnConfig.ConnectTimeout = time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	setup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := infrastructure.CheckEnvironment(setup, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(setup, "DROP SCHEMA IF EXISTS notes CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := infrastructure.Migrate(setup, pool, infrastructure.Migrations()); err != nil {
		t.Fatal(err)
	}
	var denyReader atomic.Bool
	policy := achrix.PolicyFunc(func(ctx context.Context, principal achrix.Principal, operation, resource string) error {
		if principal == "reader" && operation == application.Read && denyReader.Load() {
			return achrix.ErrDenied
		}
		return application.Policy(ctx, principal, operation, resource)
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, policy, store)
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
	privateCause := fmt.Errorf("PRIVATE_SAVED_NOTE_ACKNOWLEDGEMENT: %w", context.DeadlineExceeded)
	repository := &bridgeAcknowledgementLoss{store: store, cause: privateCause}
	var evidenceMu sync.Mutex
	var original error
	var originalRequest bridge.Request
	var originalOperation, originalPhase string
	var terminal []bridge.Diagnostic
	service := application.NewWithDiagnostics(app, repository, logger, func(ctx context.Context, operation, phase string, cause error) {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		original, originalOperation, originalPhase = cause, operation, phase
		originalRequest, _ = bridge.RequestFromContext(ctx)
	})
	writer, reader := rand.Text()+rand.Text(), rand.Text()+rand.Text()
	const endpointURL = "https://notes.example.test/mcp"
	endpoint, err := presentation.NewBridge(service, bridge.Config{
		ResourceURL: endpointURL, AuthorizationServers: []string{"https://identity.example.test"}, Scopes: []string{"notes"},
		// These two in-memory provider credentials prove verified account mapping,
		// not a production OAuth issuer, product consent, or real-client onboarding.
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			var principal achrix.Principal
			switch token {
			case writer:
				principal = "writer"
			case reader:
				principal = "reader"
			default:
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: principal, ClientID: "notes-consumer-proof", ConnectionID: "connection-" + string(principal)}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"notes"}}, nil
		},
		Admit: func(context.Context, bridge.Caller) error { return nil },
		Diagnostic: func(ctx context.Context, event bridge.Diagnostic) {
			evidenceMu.Lock()
			terminal = append(terminal, event)
			evidenceMu.Unlock()
			// Record useful trusted metadata, never serialize event.Cause into
			// ordinary logs. Protected callback storage is product-owned.
			logger.InfoContext(diagnosis.WithRequestID(ctx, event.Request.ID), "Notes Bridge evidence",
				"component", "notes.bridge", "operation", event.Request.Operation,
				"phase", event.Phase, "principal", string(event.Request.Caller.Principal),
				"client_id", event.Request.Caller.ClientID, "connection_id", event.Request.Caller.ConnectionID,
				"dispatched", event.Dispatched, "outcome", string(event.Outcome), "reference", event.Reference)
		},
		Limits: bridge.Limits{RequestBytes: 4096, ResponseBytes: 8192, Concurrent: 4, Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		IsError           bool            `json:"isError"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		Content           []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	call := func(token, operation string, arguments map[string]any) (result, *httptest.ResponseRecorder) {
		t.Helper()
		params := map[string]any{"name": operation, "arguments": arguments, "_meta": map[string]any{
			mcp.MetaKeyProtocolVersion:    "2026-07-28",
			mcp.MetaKeyClientCapabilities: map[string]any{},
			mcp.MetaKeyClientInfo:         map[string]any{"name": "notes-postgres-fixture", "version": "1"},
		}}
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, endpointURL, strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Mcp-Protocol-Version", "2026-07-28")
		request.Header.Set("Mcp-Method", "tools/call")
		request.Header.Set("Mcp-Name", operation)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Request-ID", "PRIVATE_FORGED_ID")
		response := httptest.NewRecorder()
		endpoint.ServeHTTP(response, request)
		var envelope struct {
			Result result          `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Error) != 0 {
			t.Fatal("MCP consumer request failed", response.Code, response.Body.String())
		}
		return envelope.Result, response
	}
	fault := func(value result) map[string]any {
		t.Helper()
		var failure map[string]any
		if !value.IsError || len(value.Content) != 1 || json.Unmarshal([]byte(value.Content[0].Text), &failure) != nil {
			t.Fatal("missing structured safe failure", value)
		}
		return failure
	}

	// The caller receives the unknown result's generated ID. A completely lost
	// MCP response could also lose that ID: Notes has no client operation key or
	// status ledger, so this test must not claim recovery for complete response loss.
	const text = "PRIVATE_PERSISTED_NOTE_یادداشت"
	unknown, response := call(writer, application.Create, map[string]any{"text": text})
	failure := fault(unknown)
	id, _ := failure["reference"].(string)
	action, _ := failure["action"].(string)
	requestID := response.Header().Get("X-Request-ID")
	if failure["code"] != "write_outcome_unknown" || failure["outcome"] != string(bridge.Unknown) || domain.ValidateID(id) != nil || failure["request_id"] != requestID || requestID == "" || requestID == "PRIVATE_FORGED_ID" || action == "" {
		t.Fatal("uncertain write lost useful recovery evidence", failure)
	}
	evidenceMu.Lock()
	cause, capturedRequest, operation, phase := original, originalRequest, originalOperation, originalPhase
	evidenceMu.Unlock()
	if cause != privateCause || !errors.Is(cause, context.DeadlineExceeded) || operation != application.Create || phase != "repository_save" || capturedRequest.ID != requestID || capturedRequest.Caller.Principal != "writer" || capturedRequest.Caller.ClientID != "notes-consumer-proof" || capturedRequest.Caller.ConnectionID != "connection-writer" {
		t.Fatal("protected product evidence lost cause/phase/caller/correlation", cause, operation, phase, capturedRequest)
	}
	assertOneWrite := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var count int
		var savedID string
		if err := pool.QueryRow(ctx, "SELECT count(*), min(id) FROM notes.entries").Scan(&count, &savedID); err != nil || count != 1 || savedID != id {
			t.Fatal("acknowledgement fault did not preserve exactly one real note", count, savedID, err)
		}
		if writes, _ := repository.counts(); writes != 1 {
			t.Fatal("mutation was replayed", writes)
		}
	}
	assertOneWrite()
	if strings.Contains(response.Body.String(), text) || strings.Contains(response.Body.String(), "PRIVATE_") {
		t.Fatal("uncertain result exposed private input/provider context")
	}

	denyReader.Store(true)
	denied, _ := call(reader, application.Read, map[string]any{"id": id})
	if deniedFault := fault(denied); deniedFault["code"] != "permission_denied" {
		t.Fatal("reconciliation bypassed current product permission", deniedFault)
	}
	if _, reads := repository.counts(); reads != 0 {
		t.Fatal("denied reconciliation reached persistence", reads)
	}
	denyReader.Store(false)
	read, _ := call(reader, application.Read, map[string]any{"id": id})
	var note domain.Note
	if read.IsError || json.Unmarshal(read.StructuredContent, &note) != nil || note.ID != id || note.Text != text || note.CreatedAt.IsZero() {
		t.Fatal("authorized Read did not reconcile the real saved note", read, note)
	}
	assertOneWrite()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if direct, err := service.Read(ctx, "reader", id); err != nil || direct != note {
		t.Fatal("MCP and ordinary service did not read the same persisted note", direct, err)
	}
	evidenceMu.Lock()
	confirmedRead := false
	for _, event := range terminal {
		if event.Request.Operation == application.Read && event.Dispatched && event.Outcome == bridge.Succeeded && event.Reference == id && event.Cause == nil && event.Request.Caller.Principal == "reader" {
			confirmedRead = true
		}
	}
	evidenceMu.Unlock()
	if !confirmedRead {
		t.Fatal("successful reconciliation lost attributable confirmed identity")
	}
	foundCorrelatedFailure := false
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["component"] == "notes.application" && record["phase"] == "repository_save" && record["request_id"] == requestID && record["outcome"] == "unknown" {
			foundCorrelatedFailure = true
		}
	}
	if !foundCorrelatedFailure {
		t.Fatal("ordinary log lost the protected failure's trusted correlation", logs.String())
	}
	for _, secret := range []string{writer, reader, text, dsn, "PRIVATE_", "INSERT INTO"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("private credentials, payload or provider cause entered ordinary logs")
		}
	}
}
