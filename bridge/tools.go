// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registrar binds typed tools only while New's registration callback is active.
// It delegates schemas and invocation mechanics to the official SDK; its small
// name set rejects accidental replacement rather than supplying a dispatcher.
type Registrar struct {
	mu       sync.Mutex
	server   *mcp.Server
	endpoint *Endpoint
	names    map[string]*inputDiagnostic
	open     bool
}

// AddTool binds one ordinary typed application operation. The handler obtains
// its verified Caller from RequestFromContext and must call its usual service
// authorization. Tool annotations describe effects but never grant authority.
// SDK schema inference/validation is retained. Handler errors use Fault when
// safe fields, a known outcome, or an authorized recovery reference are known.
func AddTool[In, Out any](r *Registrar, tool *mcp.Tool, handler func(context.Context, In) (Out, error)) (err error) {
	if r == nil || tool == nil || handler == nil {
		return errors.New("bridge: registrar, tool and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return errors.New("bridge: catalog registration is closed")
	}
	if !safeCode(tool.Name) {
		return errors.New("bridge: tool name must be 1–64 ASCII letters, digits, underscores, periods or hyphens")
	}
	if r.names[tool.Name] != nil {
		return fmt.Errorf("bridge: duplicate tool %q", tool.Name)
	}
	// The SDK copies Tool shallowly. Snapshot catalog metadata and explicit
	// schemas so later caller mutation cannot change discovery or validation.
	data, err := json.Marshal(tool)
	if err != nil {
		return fmt.Errorf("bridge: tool metadata: %w", err)
	}
	var fixed mcp.Tool
	if err = json.Unmarshal(data, &fixed); err != nil {
		return fmt.Errorf("bridge: tool metadata: %w", err)
	}
	input, err := prepareInput[In](&fixed)
	if err != nil {
		return fmt.Errorf("bridge: input schema for %q: %w", fixed.Name, err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("bridge: invalid tool %q: %v", fixed.Name, recovered)
		}
	}()
	mcp.AddTool(r.server, &fixed, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		s := stateFromContext(ctx)
		if s == nil {
			return nil, zero, errors.New("bridge: missing verified request")
		}
		s.mu.Lock()
		s.request.Operation = fixed.Name
		caller := s.request.Caller
		s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			setInvocationFault(s, timeoutFault(NotExecuted), err, "admission")
			return nil, zero, err
		}
		if gate := r.endpoint.config.Gate; gate != nil {
			if err := gate(ctx, caller, fixed.Name); err != nil {
				f := admissionFault(err)
				setInvocationFault(s, f, err, "gate")
				return nil, zero, errors.New(f.Message)
			}
		}
		if err := ctx.Err(); err != nil {
			setInvocationFault(s, timeoutFault(NotExecuted), err, "gate")
			return nil, zero, err
		}
		s.mu.Lock()
		s.dispatched = true
		s.outcome = Unknown
		s.mu.Unlock()
		output, err := handler(ctx, input)
		if err != nil {
			f, cause := normalizeFault(err, s.snapshot().Outcome)
			setInvocationFault(s, f, cause, "invoke")
			// Returning a plain error makes SDK skip success output validation.
			// Receiving middleware replaces it with the safe machine projection.
			return nil, zero, errors.New(f.Message)
		}
		s.mu.Lock()
		s.outcome = Succeeded
		s.mu.Unlock()
		return nil, output, nil
	})
	r.names[fixed.Name] = input
	return nil
}

func setInvocationFault(s *requestState, f *Fault, cause error, phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// An already confirmed success cannot be erased by a later failure.
	if s.outcome != Succeeded {
		s.outcome = f.Outcome
	}
	if f.Reference != "" && (s.reference == "" || s.outcome != Succeeded) {
		s.reference = f.Reference
	}
	s.fault = f
	s.cause = cause
	s.phase = phase
}

func (e *Endpoint) receive(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		s := stateFromContext(ctx)
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			if actual, ok := extra.TokenInfo.Extra["achrix.bridge.request"].(*requestState); ok {
				s = actual
			}
		}
		if s == nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "Verified request context is unavailable."}
		}
		// Use this HTTP request's bounded context for both protocol eras. Legacy
		// SDK sessions otherwise detach cancellation from their originating POST.
		ctx = s.ctx
		switch method {
		case "initialize", "notifications/initialized", "ping", "server/discover", "tools/list", "tools/call":
			s.mu.Lock()
			s.request.Operation = method
			s.mu.Unlock()
		default:
			f := &Fault{Code: "unsupported_method", Message: "This MCP method is not enabled.", Action: "Use discovery and the registered request-only tools; subscriptions and background work are not supported.", Outcome: NotExecuted}
			e.report(ctx, s, "protocol", f.Code, errors.New("method outside request-only profile"), true)
			data, _ := json.Marshal(project(f, s))
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: f.Message, Data: data}
		}
		var input *inputDiagnostic
		var arguments json.RawMessage
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
			if input = e.inputs[call.Params.Name]; input != nil {
				s.mu.Lock()
				s.request.Operation = call.Params.Name
				s.mu.Unlock()
				arguments = call.Params.Arguments
			}
		}
		result, err := next(ctx, method, req)
		s.mu.Lock()
		ownFault, ownCause, ownPhase := s.fault, s.cause, s.phase
		dispatched := s.dispatched
		s.mu.Unlock()
		if ownFault != nil {
			e.report(ctx, s, ownPhase, ownFault.Code, ownCause, true)
			return toolFailure(ownFault, s), nil
		}
		if method == "tools/call" {
			var cause error = err
			if call, ok := result.(*mcp.CallToolResult); ok && call != nil && call.IsError {
				cause = call.GetError()
				if cause == nil {
					cause = errors.New("SDK rejected tool arguments")
				}
			}
			if cause != nil {
				phase := "input"
				f := &Fault{Code: "invalid_arguments", Message: "The tool arguments do not satisfy its input contract.", Action: "Read this tool's input schema with tools/list and correct the indicated arguments.", Fields: map[string]string{"arguments": "Required fields, types and constraints must match the tool schema."}, Outcome: NotExecuted}
				if dispatched {
					phase = "output"
					f = &Fault{Code: "output_unavailable", Message: "The operation ran but its response could not be produced.", Action: "Use the known outcome and authorized reference to reconcile the result; do not automatically repeat a write.", Outcome: s.snapshot().Outcome}
				} else if input != nil {
					fields, validationCause := input.details(arguments)
					if len(fields) > 0 {
						f.Fields = fields
					}
					if validationCause != nil {
						cause = errors.Join(cause, validationCause)
					}
				} else {
					f.Code = "unknown_tool"
					f.Message = "The requested tool is not registered."
					f.Action = "Use tools/list to select an available operation."
					f.Fields = nil
				}
				e.report(ctx, s, phase, f.Code, cause, true)
				return toolFailure(f, s), nil
			}
		}
		if err != nil {
			f := &Fault{Code: "invalid_request", Message: "The MCP request could not be processed.", Action: "Check this endpoint's supported protocol version and request contract.", Outcome: NotExecuted}
			e.report(ctx, s, "protocol", f.Code, err, true)
			code := int64(jsonrpc.CodeInvalidParams)
			var wire *jsonrpc.Error
			if errors.As(err, &wire) {
				code = wire.Code
			}
			data, _ := json.Marshal(project(f, s))
			return nil, &jsonrpc.Error{Code: code, Message: f.Message, Data: data}
		}
		e.report(ctx, s, "complete", "ok", nil, true)
		return result, nil
	}
}
