// SPDX-License-Identifier: MPL-2.0
package presentation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const notesBridgeWriter = "PRIVATE_NOTES_BRIDGE_WRITER_TOKEN"
const notesBridgeReader = "PRIVATE_NOTES_BRIDGE_READER_TOKEN"
const notesBridgeURL = "https://notes.example.test/mcp"
const forgedBridgeRequestID = "PRIVATE_FORGED_REQUEST_ID"

type notesBridgeRepository struct {
	mu     sync.Mutex
	notes  map[string]domain.Note
	writes int
	cause  error
}

func (r *notesBridgeRepository) Save(_ context.Context, note domain.Note) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	if r.cause != nil {
		return r.cause
	}
	if r.notes == nil {
		r.notes = make(map[string]domain.Note)
	}
	r.notes[note.ID] = note
	return nil
}
func (r *notesBridgeRepository) Find(_ context.Context, id string) (domain.Note, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ok := r.notes[id]
	if !ok {
		return domain.Note{}, domain.ErrNotFound
	}
	return note, nil
}
func (r *notesBridgeRepository) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

type notesBridgeLogs struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *notesBridgeLogs) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}
func (b *notesBridgeLogs) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

type notesBridgeResult struct {
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	Content           []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type notesBridgeFailure struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields"`
	Outcome   bridge.Outcome    `json:"outcome"`
	Reference string            `json:"reference"`
	RequestID string            `json:"request_id"`
}

func notesBridgeRPC(t *testing.T, handler http.Handler, token, method string, params map[string]any) (*httptest.ResponseRecorder, json.RawMessage) {
	t.Helper()
	params["_meta"] = map[string]any{
		mcp.MetaKeyProtocolVersion:    "2026-07-28",
		mcp.MetaKeyClientCapabilities: map[string]any{},
		mcp.MetaKeyClientInfo:         map[string]any{"name": "notes-permission-fixture", "version": "1"},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, notesBridgeURL, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("Mcp-Name", name)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", forgedBridgeRequestID)
	request.Header.Set("X-Principal", "writer")
	request.Header.Set("X-Client-ID", "PRIVATE_FORGED_CLIENT")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") == "" || response.Header().Get("X-Request-ID") == forgedBridgeRequestID {
		t.Fatal("transport did not supply trusted correlation")
	}
	if response.Code != http.StatusOK {
		return response, nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Error) != 0 || len(envelope.Result) == 0 {
		t.Fatal("unexpected MCP envelope", response.Body.String(), err)
	}
	return response, envelope.Result
}

func callNotesBridge(t *testing.T, handler http.Handler, token, operation string, arguments map[string]any) (notesBridgeResult, *httptest.ResponseRecorder) {
	t.Helper()
	response, result := notesBridgeRPC(t, handler, token, "tools/call", map[string]any{"name": operation, "arguments": arguments})
	if response.Code != http.StatusOK {
		t.Fatal("tool call did not reach MCP", response.Code, response.Body.String())
	}
	var decoded notesBridgeResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded, response
}

func notesBridgeNote(t *testing.T, result notesBridgeResult) domain.Note {
	t.Helper()
	var note domain.Note
	if result.IsError || json.Unmarshal(result.StructuredContent, &note) != nil || note.ID == "" || note.Text == "" || note.CreatedAt.IsZero() {
		t.Fatal("authorized note data was lost", result)
	}
	return note
}

func notesBridgeFaultResult(t *testing.T, result notesBridgeResult, response *httptest.ResponseRecorder) notesBridgeFailure {
	t.Helper()
	var failure notesBridgeFailure
	if !result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || json.Unmarshal([]byte(result.Content[0].Text), &failure) != nil {
		t.Fatal("missing actionable tool failure", result)
	}
	if failure.Code == "" || failure.Message == "" || failure.Action == "" || failure.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatal("failure lost meaning/action/trusted correlation", failure)
	}
	return failure
}

func notesBridgeConfiguration() bridge.Config {
	return bridge.Config{
		ResourceURL: notesBridgeURL, AuthorizationServers: []string{"https://identity.example.test"}, Scopes: []string{"notes"},
		// This fixture exercises the provider seam. It is not an OAuth issuer or
		// evidence of a production login, consent, refresh, or account-linking flow.
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			var principal achrix.Principal
			switch token {
			case notesBridgeWriter:
				principal = "writer"
			case notesBridgeReader:
				principal = "reader"
			default:
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return bridge.Credential{Caller: bridge.Caller{Principal: principal, ClientID: "notes-fixture-client", ConnectionID: "connection-" + string(principal)}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"notes"}}, nil
		},
		Admit:      func(context.Context, bridge.Caller) error { return nil },
		Diagnostic: func(context.Context, bridge.Diagnostic) {},
		Limits:     bridge.Limits{RequestBytes: 4096, ResponseBytes: 8192, Concurrent: 4, Timeout: 2 * time.Second},
	}
}

func notesBridgeApplication(t *testing.T, repository application.Repository, policy achrix.Policy, diagnostic func(context.Context, string, string, error)) (*achrix.Application, *application.Service, *notesBridgeLogs) {
	t.Helper()
	logs := &notesBridgeLogs{}
	logger := diagnosis.New(logs)
	t.Cleanup(func() { diagnosis.Flush(logger) })
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: logger}, policy, &testModule{})
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
	return app, application.NewWithDiagnostics(app, repository, logger, diagnostic), logs
}

func TestBridgeNotesUsesCurrentProductPermissions(t *testing.T) {
	repository := &notesBridgeRepository{}
	var blockWriterCreate, blockReaderRead, blockChannelCreate, disabled atomic.Bool
	policy := achrix.PolicyFunc(func(ctx context.Context, principal achrix.Principal, operation, resource string) error {
		if principal == "writer" && operation == application.Create && blockWriterCreate.Load() || principal == "reader" && operation == application.Read && blockReaderRead.Load() {
			return achrix.ErrDenied
		}
		return application.Policy(ctx, principal, operation, resource)
	})
	app, service, _ := notesBridgeApplication(t, repository, policy, nil)
	ordinary := presentation.New(app, service, notesBridgeWriter, notesBridgeReader, nil)
	httpRequest := func(method, path, token, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		ordinary.ServeHTTP(response, request)
		return response
	}
	configuration := notesBridgeConfiguration()
	var evidenceMu sync.Mutex
	var evidence []bridge.Diagnostic
	configuration.Diagnostic = func(_ context.Context, event bridge.Diagnostic) {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		evidence = append(evidence, event)
	}
	configuration.Admit = func(context.Context, bridge.Caller) error {
		if disabled.Load() {
			return achrix.ErrDenied
		}
		return nil
	}
	configuration.Gate = func(_ context.Context, _ bridge.Caller, operation string) error {
		if operation == application.Create && blockChannelCreate.Load() {
			return achrix.ErrDenied
		}
		return nil
	}
	endpoint, err := presentation.NewBridge(service, configuration)
	if err != nil {
		t.Fatal(err)
	}
	created, _ := callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "Authorized note through MCP"})
	note := notesBridgeNote(t, created)
	evidenceMu.Lock()
	confirmedCreate := false
	for _, event := range evidence {
		if event.Dispatched && event.Outcome == bridge.Succeeded && event.Reference == note.ID && event.Cause == nil && event.Request.Operation == application.Create && event.Request.Caller.Principal == "writer" && event.Request.Caller.ClientID == "notes-fixture-client" {
			confirmedCreate = true
		}
	}
	evidenceMu.Unlock()
	if !confirmedCreate {
		t.Fatal("confirmed create lost its authorized resource or caller identity")
	}
	read, _ := callNotesBridge(t, endpoint, notesBridgeReader, application.Read, map[string]any{"id": note.ID})
	if got := notesBridgeNote(t, read); got.ID != note.ID || got.Text != note.Text {
		t.Fatal("reader did not see the authoritative note", got)
	}
	if response := httpRequest(http.MethodGet, "/notes/"+note.ID, notesBridgeReader, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), note.Text) {
		t.Fatal("ordinary HTTP did not share the same reader authority", response.Code, response.Body.String())
	}
	before := repository.writeCount()
	denied, response := callNotesBridge(t, endpoint, notesBridgeReader, application.Create, map[string]any{"text": "denied"})
	if failure := notesBridgeFaultResult(t, denied, response); failure.Code != "permission_denied" || failure.Outcome != bridge.NotExecuted {
		t.Fatal(failure)
	}
	if response := httpRequest(http.MethodPost, "/notes", notesBridgeReader, `{"text":"denied"}`); response.Code != http.StatusForbidden || repository.writeCount() != before {
		t.Fatal("reader gained a second permission path", response.Code, repository.writeCount())
	}

	// Catalog visibility is not a grant. The ordinary policy changes after
	// discovery, while the endpoint, credentials and client remain unchanged.
	response, catalog := notesBridgeRPC(t, endpoint, notesBridgeWriter, "tools/list", map[string]any{})
	if response.Code != http.StatusOK || !strings.Contains(string(catalog), application.Create) || !strings.Contains(string(catalog), application.Read) {
		t.Fatal("missing deliberate Notes catalog", response.Code, string(catalog))
	}
	blockWriterCreate.Store(true)
	denied, response = callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "revoked"})
	if failure := notesBridgeFaultResult(t, denied, response); failure.Code != "permission_denied" {
		t.Fatal("discovery froze old permissions", failure)
	}
	if response := httpRequest(http.MethodPost, "/notes", notesBridgeWriter, `{"text":"revoked"}`); response.Code != http.StatusForbidden {
		t.Fatal("HTTP used different current permissions", response.Code)
	}
	if _, err := service.Create(context.Background(), "writer", "revoked"); !errors.Is(err, achrix.ErrDenied) || repository.writeCount() != before {
		t.Fatal("direct service bypassed current permissions", err, repository.writeCount())
	}
	blockWriterCreate.Store(false)
	created, _ = callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "Allowed after current policy changed"})
	_ = notesBridgeNote(t, created)

	blockChannelCreate.Store(true)
	before = repository.writeCount()
	denied, response = callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "channel denied"})
	if failure := notesBridgeFaultResult(t, denied, response); failure.Outcome != bridge.NotExecuted || repository.writeCount() != before {
		t.Fatal("channel restriction did not stop the write", failure, repository.writeCount())
	}
	if response := httpRequest(http.MethodPost, "/notes", notesBridgeWriter, `{"text":"Still allowed through ordinary HTTP"}`); response.Code != http.StatusCreated {
		t.Fatal("channel restriction changed ordinary product authority", response.Code)
	}
	blockChannelCreate.Store(false)
	created, _ = callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "Allowed after channel restriction changed"})
	_ = notesBridgeNote(t, created)

	disabled.Store(true)
	before = repository.writeCount()
	response, _ = notesBridgeRPC(t, endpoint, notesBridgeWriter, "tools/call", map[string]any{"name": application.Create, "arguments": map[string]any{"text": "disabled"}})
	if response.Code != http.StatusForbidden || repository.writeCount() != before {
		t.Fatal("administrator disablement admitted an operation", response.Code, repository.writeCount())
	}
	if response := httpRequest(http.MethodPost, "/notes", notesBridgeWriter, `{"text":"Ordinary HTTP remains enabled"}`); response.Code != http.StatusCreated {
		t.Fatal("MCP disablement changed ordinary HTTP authority", response.Code)
	}
	disabled.Store(false)
	blockReaderRead.Store(true)
	denied, response = callNotesBridge(t, endpoint, notesBridgeReader, application.Read, map[string]any{"id": note.ID})
	if failure := notesBridgeFaultResult(t, denied, response); failure.Code != "permission_denied" {
		t.Fatal("read permission change was ignored", failure)
	}
	if response := httpRequest(http.MethodGet, "/notes/"+note.ID, notesBridgeReader, ""); response.Code != http.StatusForbidden {
		t.Fatal("HTTP ignored the same read permission change", response.Code)
	}
	blockReaderRead.Store(false)
	read, _ = callNotesBridge(t, endpoint, notesBridgeReader, application.Read, map[string]any{"id": note.ID})
	_ = notesBridgeNote(t, read)
}

func TestBridgeNotesUsefulFieldsAndWriteOutcomes(t *testing.T) {
	privateCause := fmt.Errorf("PRIVATE_PERSISTENCE_ACKNOWLEDGEMENT: %w", context.DeadlineExceeded)
	for _, tc := range []struct {
		name    string
		cause   error
		outcome bridge.Outcome
	}{
		{"not_executed", domain.ErrNotExecuted, bridge.NotExecuted},
		{"not_committed", domain.ErrNotCommitted, bridge.NotCommitted},
		{"unknown", privateCause, bridge.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &notesBridgeRepository{cause: tc.cause}
			var mu sync.Mutex
			var captured error
			var capturedRequest bridge.Request
			var capturedOperation, capturedPhase string
			_, service, logs := notesBridgeApplication(t, repository, achrix.PolicyFunc(application.Policy), func(ctx context.Context, operation, phase string, cause error) {
				mu.Lock()
				defer mu.Unlock()
				captured, capturedOperation, capturedPhase = cause, operation, phase
				capturedRequest, _ = bridge.RequestFromContext(ctx)
			})
			endpoint, err := presentation.NewBridge(service, notesBridgeConfiguration())
			if err != nil {
				t.Fatal(err)
			}
			result, response := callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": "PRIVATE_NOTE_PAYLOAD"})
			failure := notesBridgeFaultResult(t, result, response)
			if failure.Outcome != tc.outcome || repository.writeCount() != 1 {
				t.Fatal("write classification or attempt count changed", failure, repository.writeCount())
			}
			if tc.outcome == bridge.Unknown {
				mu.Lock()
				cause, request, operation, phase := captured, capturedRequest, capturedOperation, capturedPhase
				mu.Unlock()
				if failure.Code != "write_outcome_unknown" || domain.ValidateID(failure.Reference) != nil || cause != privateCause || operation != application.Create || phase != "repository_save" {
					t.Fatal("unknown write lost identity, cause, or phase", failure, cause, operation, phase)
				}
				if request.ID != failure.RequestID || request.Caller.Principal != "writer" || request.Caller.ClientID != "notes-fixture-client" || request.Caller.ConnectionID != "connection-writer" || request.Operation != application.Create {
					t.Fatal("product diagnostic lost trusted caller/correlation", request)
				}
				found := false
				for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
					var record map[string]any
					if json.Unmarshal([]byte(line), &record) == nil && record["component"] == "notes.application" && record["phase"] == "repository_save" && record["request_id"] == failure.RequestID && record["outcome"] == "unknown" {
						found = true
					}
				}
				if !found {
					t.Fatal("ordinary Notes log lost useful trusted correlation", logs.String())
				}
			} else if failure.Reference != "" {
				t.Fatal("non-executed/noncommitted write invented a retained note", failure)
			}
			if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(logs.String(), "PRIVATE_") {
				t.Fatal("private payload, credentials, header or provider cause escaped")
			}
		})
	}

	repository := &notesBridgeRepository{}
	_, service, _ := notesBridgeApplication(t, repository, achrix.PolicyFunc(application.Policy), nil)
	endpoint, err := presentation.NewBridge(service, notesBridgeConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	result, response := callNotesBridge(t, endpoint, notesBridgeWriter, application.Create, map[string]any{"text": " \t "})
	failure := notesBridgeFaultResult(t, result, response)
	if failure.Code != "invalid_input" || failure.Fields["field"] != "text" || !strings.Contains(failure.Fields["detail"], "1..200") || failure.Outcome != bridge.NotExecuted || repository.writeCount() != 0 {
		t.Fatal("field correction was erased or invalid input wrote state", failure, repository.writeCount())
	}
}
