// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/AChWorks/achrix"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Outcome is evidence about the operation, independently of response delivery.
type Outcome string

const (
	NotExecuted  Outcome = "not_executed"
	NotCommitted Outcome = "not_committed"
	Succeeded    Outcome = "succeeded"
	Unknown      Outcome = "unknown"
)

// Fault is a product-approved safe transport projection plus a protected cause.
// Message, Action, Fields and Reference must already be suitable for this
// caller. Cause is never serialized and is intentionally not an unwrap chain
// exposed to remote clients. A post-dispatch ordinary error defaults to Unknown;
// only owning operation evidence may establish NotExecuted or NotCommitted.
type Fault struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields,omitempty"`
	Outcome   Outcome           `json:"outcome"`
	Reference string            `json:"reference,omitempty"`
	Cause     error             `json:"-"`
	// HTTPStatus optionally selects a safe 400..599 JSON HTTP status. MCP tool
	// failures ignore it and retain their ordinary isError representation.
	HTTPStatus int `json:"-"`
}

func (f *Fault) Error() string {
	if f == nil {
		return "bridge operation failed"
	}
	return f.Message
}

// Request contains only Bridge-generated or verifier-bound attribution.
// Operation is a fixed binding name while its handler runs. Channel is "mcp"
// or "http", selected by the endpoint rather than any client claim.
type Request struct {
	ID        string
	Caller    Caller
	Channel   string
	Operation string
}

// Diagnostic is protected evidence, not a public error or an atomic audit event.
// A terminal operation event has Phase "complete" on success, "invoke" on an
// application error, "input" before dispatch, or "output" on SDK output failure.
// A separate "delivery" failure retains the actual known operation outcome.
type Diagnostic struct {
	Request    Request
	Phase      string
	Dispatched bool
	Outcome    Outcome
	Code       string
	Reference  string
	Cause      error
}

type requestKey struct{}

type requestState struct {
	endpoint   *Endpoint
	mu         sync.Mutex
	ctx        context.Context
	request    Request
	dispatched bool
	outcome    Outcome
	reference  string
	fault      *Fault
	phase      string
	cause      error
	reported   bool
	bodyCause  error
}

func stateFromContext(ctx context.Context) *requestState {
	s, _ := ctx.Value(requestKey{}).(*requestState)
	return s
}

// RequestFromContext lets ordinary application diagnostics share trusted
// request/user/client attribution. The returned value is an independent copy.
func RequestFromContext(ctx context.Context) (Request, bool) {
	s := stateFromContext(ctx)
	if s == nil {
		return Request{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request, true
}

// Confirm records an already confirmed success and an authorized recovery
// reference before output serialization. It performs no mutation or lookup.
// Calling it is a trusted product assertion, not client-supplied evidence.
// References must be at most 256 bytes with no control characters.
func Confirm(ctx context.Context, reference string) {
	s := stateFromContext(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dispatched {
		return
	}
	s.outcome = Succeeded
	if boundedText(reference, 256) {
		s.reference = reference
	}
}

func (s *requestState) snapshot() Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Diagnostic{Request: s.request, Dispatched: s.dispatched, Outcome: s.outcome, Reference: s.reference}
}

func (e *Endpoint) report(ctx context.Context, s *requestState, phase, code string, cause error, terminal bool) {
	s.mu.Lock()
	if terminal && s.reported {
		s.mu.Unlock()
		return
	}
	if terminal {
		s.reported = true
	}
	d := Diagnostic{Request: s.request, Phase: phase, Dispatched: s.dispatched, Outcome: s.outcome, Code: code, Reference: s.reference, Cause: cause}
	s.mu.Unlock()
	e.config.Diagnostic(ctx, d)
}

type publicFault struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Action    string            `json:"action"`
	Fields    map[string]string `json:"fields,omitempty"`
	Outcome   Outcome           `json:"outcome"`
	Reference string            `json:"reference,omitempty"`
	RequestID string            `json:"request_id"`
}

func project(f *Fault, s *requestState) publicFault {
	d := s.snapshot()
	return publicFault{Code: f.Code, Message: f.Message, Action: f.Action, Fields: copyFields(f.Fields), Outcome: d.Outcome, Reference: d.Reference, RequestID: d.Request.ID}
}

func toolFailure(f *Fault, s *requestState) *mcp.CallToolResult {
	data, _ := json.Marshal(project(f, s)) // only bounded strings/maps; cannot fail
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func copyFields(fields map[string]string) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	copy := make(map[string]string, len(fields))
	for k, v := range fields {
		copy[k] = v
	}
	return copy
}

func validFault(f *Fault) bool {
	if f != nil && f.HTTPStatus != 0 && (f.HTTPStatus < 400 || f.HTTPStatus > 599) {
		return false
	}
	if f == nil || !safeCode(f.Code) || f.Message == "" || f.Action == "" || !boundedText(f.Message, 512) || !boundedText(f.Action, 512) || !boundedText(f.Reference, 256) || len(f.Fields) > 16 {
		return false
	}
	if f.Outcome != "" && f.Outcome != NotExecuted && f.Outcome != NotCommitted && f.Outcome != Succeeded && f.Outcome != Unknown {
		return false
	}
	for name, value := range f.Fields {
		if name == "" || !boundedText(name, 64) || !boundedText(value, 256) {
			return false
		}
	}
	return true
}

func safeCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func normalizeFault(err error, outcome Outcome) (*Fault, error) {
	var f *Fault
	cause := err
	if errors.As(err, &f) && f != nil && f.Cause != nil {
		cause = f.Cause
	}
	if errors.As(err, &f) && validFault(f) {
		copy := *f
		copy.Fields = copyFields(f.Fields)
		if copy.Outcome == "" {
			copy.Outcome = outcome
		}
		copy.Cause = nil
		return &copy, cause
	}
	if errors.Is(err, achrix.ErrDenied) {
		return &Fault{Code: "denied", Message: "This operation is not permitted.", Action: "Ask the product administrator to check your current access.", Outcome: outcome}, cause
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return timeoutFault(outcome), cause
	}
	return &Fault{Code: "operation_unavailable", Message: "The operation could not produce a confirmed result.", Action: "Check the operation state before attempting a write again; provide the request ID to an authorized operator.", Outcome: outcome}, cause
}

func admissionFault(err error) *Fault {
	if errors.Is(err, achrix.ErrDenied) {
		return &Fault{Code: "denied", Message: "This external connection is not permitted.", Action: "Ask the product administrator to enable this connection and client.", Outcome: NotExecuted}
	}
	return &Fault{Code: "admission_unavailable", Message: "Connection admission could not be checked.", Action: "Contact an authorized operator with the request ID. No operation was invoked.", Outcome: NotExecuted}
}

func timeoutFault(outcome Outcome) *Fault {
	return &Fault{Code: "deadline_exceeded", Message: "The request ended before a complete response was available.", Action: "Check the known outcome and reconcile any write before retrying.", Outcome: outcome}
}

func httpStatus(f *Fault) int {
	if f.HTTPStatus >= 400 && f.HTTPStatus <= 599 {
		return f.HTTPStatus
	}
	if f.Code == "denied" {
		return 403
	}
	return 503
}
