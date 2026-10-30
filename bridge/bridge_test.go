// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func unitConfig() Config {
	return Config{
		ResourceURL:          "https://product.example/mcp",
		AuthorizationServers: []string{"https://issuer.example"},
		Scopes:               []string{"product"},
		Verify: func(context.Context, string) (Credential, error) {
			return Credential{Caller: Caller{Principal: achrix.Principal("user"), ClientID: "client"}, ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"product"}}, nil
		},
		Admit:      func(context.Context, Caller) error { return nil },
		Diagnostic: func(context.Context, Diagnostic) {},
	}
}

func TestConfigurationRejectsUnsafeOrUnboundedComposition(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"missing verifier", func(c *Config) { c.Verify = nil }},
		{"missing admission", func(c *Config) { c.Admit = nil }},
		{"missing protected diagnostics", func(c *Config) { c.Diagnostic = nil }},
		{"public plain HTTP", func(c *Config) { c.ResourceURL = "http://product.example/mcp" }},
		{"resource credentials", func(c *Config) { c.ResourceURL = "https://secret@product.example/mcp" }},
		{"resource query", func(c *Config) { c.ResourceURL = "https://product.example/mcp?access_token=secret" }},
		{"path traversal", func(c *Config) { c.ResourceURL = "https://product.example/a/../mcp" }},
		{"missing issuer", func(c *Config) { c.AuthorizationServers = nil }},
		{"unsafe issuer", func(c *Config) { c.AuthorizationServers = []string{"javascript:alert(1)"} }},
		{"scope injection", func(c *Config) { c.Scopes = []string{"read\r\nHeader: injected"} }},
		{"wildcard origin", func(c *Config) { c.AllowedOrigins = []string{"*"} }},
		{"negative input bound", func(c *Config) { c.Limits.RequestBytes = -1 }},
		{"negative concurrency", func(c *Config) { c.Limits.Concurrent = -1 }},
		{"unbounded deadline", func(c *Config) { c.Limits.Timeout = time.Hour }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := unitConfig()
			tc.change(&config)
			called := false
			_, err := New(config, func(*Registrar) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("unsafe configuration accepted or registration started: err=%v called=%v", err, called)
			}
		})
	}
	config := unitConfig()
	config.ResourceURL = "http://127.0.0.1:8123/mcp"
	config.AuthorizationServers = []string{"http://localhost:8124"}
	if _, err := New(config, func(*Registrar) error { return nil }); err != nil {
		t.Fatalf("explicit loopback fixture rejected: %v", err)
	}
}

func TestCatalogIsFixedAndMetadataCopied(t *testing.T) {
	config := unitConfig()
	input := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}
	tool := &mcp.Tool{Name: "notes.create", InputSchema: input, Description: "Create a bounded note."}
	type argument struct {
		Text string `json:"text"`
	}
	var registration *Registrar
	endpoint, err := New(config, func(r *Registrar) error {
		registration = r
		if err := AddTool(r, tool, func(context.Context, argument) (argument, error) { return argument{}, nil }); err != nil {
			return err
		}
		if err := AddTool(r, tool, func(context.Context, argument) (argument, error) { return argument{}, nil }); err == nil {
			t.Fatal("duplicate registration replaced a tool")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := AddTool(registration, &mcp.Tool{Name: "late"}, func(context.Context, argument) (argument, error) { return argument{}, nil }); err == nil {
		t.Fatal("registration accepted after construction")
	}
	config.AuthorizationServers[0] = "https://attacker.example"
	config.Scopes[0] = "admin"
	input["required"] = []string{"actor"}
	tool.Name = "renamed"
	if endpoint.config.AuthorizationServers[0] != "https://issuer.example" || endpoint.config.Scopes[0] != "product" {
		t.Fatal("mutable caller configuration changed the endpoint")
	}
	fields, _ := endpoint.inputs["notes.create"].details(json.RawMessage(`{}`))
	if !strings.Contains(fields["text"], "required") {
		t.Fatalf("mutated source schema changed fixed input contract: %#v", fields)
	}
}

func TestInputDiagnosticsKeepDiscriminatingFieldsWithoutValues(t *testing.T) {
	type input struct {
		Text string `json:"text"`
	}
	diagnostic, err := prepareInput[input](&mcp.Tool{Name: "notes.create"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, body, field, want string }{
		{"required", `{}`, "text", "required"},
		{"type", `{"text":{"private":"secret-value-canary"}}`, "text", "string"},
		{"unknown actor", `{"text":"ok","actor":"secret-value-canary"}`, "actor", "additionalProperties"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields, cause := diagnostic.details(json.RawMessage(tc.body))
			if cause == nil || !strings.Contains(fields[tc.field], tc.want) {
				t.Fatalf("missing useful field evidence: fields=%#v cause=%v", fields, cause)
			}
			wire, _ := json.Marshal(fields)
			if strings.Contains(string(wire), "secret-value-canary") {
				t.Fatalf("raw value leaked in field guidance: %s", wire)
			}
			if tc.name == "type" && !strings.Contains(cause.Error(), "secret-value-canary") {
				t.Fatal("protected original validation evidence was erased")
			}
		})
	}
}

func TestFaultProjectionPreservesCauseAndConfirmedSuccess(t *testing.T) {
	private := errors.New("provider response contains secret-value-canary")
	original := &Fault{Code: "unknown_write", Message: "Write acknowledgement was lost.", Action: "Read the authorized note ID to reconcile before repeating this write.", Fields: map[string]string{"note_id": "Use the authorized reference."}, Outcome: Unknown, Reference: "note-unknown", Cause: private}
	projected, cause := normalizeFault(fmt.Errorf("application wrapper: %w", original), Unknown)
	if cause != private {
		t.Fatal("protected provider error identity was discarded")
	}
	original.Fields["note_id"] = "mutated"
	if projected.Fields["note_id"] == "mutated" {
		t.Fatal("safe field map aliases caller storage")
	}
	s := &requestState{request: Request{ID: "trusted-request", Channel: "mcp", Operation: "notes.create"}, dispatched: true, outcome: Unknown}
	ctx := context.WithValue(context.Background(), requestKey{}, s)
	Confirm(ctx, "note-committed")
	setInvocationFault(s, projected, cause, "invoke")
	result := toolFailure(projected, s)
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "secret-value-canary") || strings.Contains(string(data), "mutated") {
		t.Fatalf("private or mutable evidence escaped: %s", data)
	}
	var public publicFault
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &public); err != nil {
		t.Fatal(err)
	}
	if public.Outcome != Succeeded || public.Reference != "note-committed" || public.RequestID != "trusted-request" || !s.snapshot().Dispatched {
		t.Fatalf("later failure erased confirmed success: %#v", public)
	}
	if result.StructuredContent != nil {
		t.Fatal("error projection must not pretend to satisfy a tool's typed success schema")
	}
}

func TestInvalidPublicFaultStillRetainsProtectedCause(t *testing.T) {
	private := errors.New("private-provider-cause")
	f, cause := normalizeFault(&Fault{Code: "invalid\ncode", Message: "bad", Action: "fix", Outcome: Unknown, Cause: private}, Unknown)
	if cause != private || f.Code != "operation_unavailable" {
		t.Fatalf("invalid projection lost cause or escaped validation: %v %v", f, cause)
	}
}
