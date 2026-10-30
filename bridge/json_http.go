// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
)

// NewJSONHTTP composes an authenticated, bounded JSON HTTP resource server.
// ResourceURL identifies its canonical path prefix and resource audience. Bind
// explicit routes to JSONHandler; this constructor never discovers operations,
// starts an MCP server, or acquires a connection/session/background worker.
//
// Verify must verify credentials for this HTTP resource, independently of a
// sibling MCP resource's audience. Admit and each fixed operation's Gate remain
// current per request; neither replaces the owning Service's authorization.
// The profile buffers bounded JSON responses. It is not a binary/streaming file
// transfer profile. The enclosing server owns TLS, header and ingress limits.
func NewJSONHTTP(config Config, handler http.Handler) (*Endpoint, error) {
	if handler == nil {
		return nil, errors.New("bridge: a JSON HTTP route handler is required")
	}
	e, err := newEndpoint(config)
	if err != nil {
		return nil, err
	}
	e.jsonHTTP = true
	e.handler = e.protect(handler)
	return e, nil
}

// JSONHandler binds one fixed, typed HTTP operation. Mount it deliberately in a
// product/component router under NewJSONHTTP. Decode must cheaply validate all
// transport input without effects, sensitive lookups or trusting identity claims.
// Invoke obtains verified attribution through RequestFromContext and calls the
// ordinary typed Service with its normal current authorization. Both callbacks
// must be concurrency-safe and honor the request context and published bounds.
//
// A Fault supplies approved public guidance and an optional HTTPStatus (400..599).
// Its private Cause remains protected. An error after Invoke defaults to Unknown;
// use the owning service's actual outcome, and Confirm for a success reference.
// No partial typed output is used on error; retain reconciliation data in Fault.
func JSONHandler[In, Out any](operation string, decode func(*http.Request) (In, error), invoke func(context.Context, In) (Out, error)) (http.Handler, error) {
	if !safeCode(operation) || decode == nil || invoke == nil {
		return nil, errors.New("bridge: a bounded fixed operation name, decoder and invocation are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		s := stateFromContext(ctx)
		if s == nil || s.endpoint == nil || !s.endpoint.jsonHTTP || s.request.Caller.Principal == "" {
			// This is a composition error, not an alternate authentication path.
			// No decoder, gate or application callback is run outside the boundary.
			http.Error(w, "A verified JSON HTTP endpoint is required.", http.StatusInternalServerError)
			return
		}
		e := s.endpoint
		s.mu.Lock()
		s.request.Operation = operation
		caller := s.request.Caller
		s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			e.failHTTP(w, r, s, http.StatusServiceUnavailable, "input", timeoutFault(NotExecuted), err)
			return
		}
		input, err := decode(r)
		if err != nil {
			f, phase, cause := jsonInputFault(err, s)
			setInvocationFault(s, f, cause, phase)
			e.failHTTP(w, r, s, httpStatus(f), phase, f, cause)
			return
		}
		if err := ctx.Err(); err != nil {
			e.failHTTP(w, r, s, http.StatusServiceUnavailable, "input", timeoutFault(NotExecuted), err)
			return
		}
		if gate := e.config.Gate; gate != nil {
			if err := gate(ctx, caller, operation); err != nil {
				f := admissionFault(err)
				setInvocationFault(s, f, err, "gate")
				e.failHTTP(w, r, s, httpStatus(f), "gate", f, err)
				return
			}
		}
		if err := ctx.Err(); err != nil {
			e.failHTTP(w, r, s, http.StatusServiceUnavailable, "gate", timeoutFault(NotExecuted), err)
			return
		}
		s.mu.Lock()
		s.dispatched, s.outcome = true, Unknown
		s.mu.Unlock()
		output, err := invoke(ctx, input)
		if err != nil {
			f, cause := normalizeFault(err, s.snapshot().Outcome)
			setInvocationFault(s, f, cause, "invoke")
			e.failHTTP(w, r, s, httpStatus(f), "invoke", f, cause)
			return
		}
		s.mu.Lock()
		s.outcome = Succeeded
		s.mu.Unlock()
		data, err := json.Marshal(output)
		if err != nil {
			f := &Fault{Code: "output_unavailable", Message: "The operation succeeded but its JSON response could not be produced.", Action: "Use the known outcome and authorized reference to reconcile; do not repeat a write automatically.", Outcome: Succeeded}
			e.failHTTP(w, r, s, http.StatusInternalServerError, "output", f, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data) // The endpoint owns bounded buffering and actual delivery.
		e.report(ctx, s, "complete", "ok", nil, true)
	}), nil
}

func jsonInputFault(err error, s *requestState) (*Fault, string, error) {
	s.mu.Lock()
	bodyCause := s.bodyCause
	s.mu.Unlock()
	cause := err
	if bodyCause != nil {
		cause = bodyCause
	}
	var tooLarge *http.MaxBytesError
	if errors.As(cause, &tooLarge) {
		return &Fault{Code: "request_too_large", Message: "The request exceeds the endpoint's input limit.", Action: "Reduce the complete request body to the operation's published limit.", Outcome: NotExecuted, HTTPStatus: http.StatusRequestEntityTooLarge}, "body_read", cause
	}
	if errors.Is(cause, context.Canceled) {
		return &Fault{Code: "request_canceled", Message: "The request was canceled before its input could be accepted.", Action: "Start a new complete request when the caller is ready. No operation was invoked.", Outcome: NotExecuted, HTTPStatus: http.StatusServiceUnavailable}, "input", cause
	}
	var timeout net.Error
	if errors.Is(cause, context.DeadlineExceeded) || errors.As(cause, &timeout) && timeout.Timeout() {
		return &Fault{Code: "body_timeout", Message: "The request input did not arrive within its deadline.", Action: "Send the complete bounded request within the deadline. No operation was invoked.", Outcome: NotExecuted, HTTPStatus: http.StatusRequestTimeout}, "body_read", cause
	}
	if bodyCause != nil && !errors.Is(bodyCause, io.EOF) {
		return &Fault{Code: "body_unavailable", Message: "The request body could not be read completely.", Action: "Check the connection and send the complete bounded request. No operation was invoked.", Outcome: NotExecuted, HTTPStatus: http.StatusBadRequest}, "body_read", cause
	}
	var supplied *Fault
	if errors.As(err, &supplied) && validFault(supplied) {
		f, cause := normalizeFault(err, NotExecuted)
		f.Outcome = NotExecuted
		if f.HTTPStatus == 0 {
			f.HTTPStatus = http.StatusBadRequest
		}
		return f, "input", cause
	}
	return &Fault{Code: "invalid_input", Message: "The request does not satisfy this operation's input contract.", Action: "Check the published path, parameters, body type and field constraints.", Outcome: NotExecuted, HTTPStatus: http.StatusBadRequest}, "input", cause
}

func (e *Endpoint) validateJSONHTTP(r *http.Request) (int, *Fault) {
	failure := func(status int, code, message, action string) (int, *Fault) {
		return status, &Fault{Code: code, Message: message, Action: action, Outcome: NotExecuted}
	}
	prefix := strings.TrimSuffix(e.resource.Path, "/")
	if r.URL.RawPath != "" || r.URL.Path == "" || path.Clean(r.URL.Path) != r.URL.Path ||
		prefix != "" && r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return failure(http.StatusBadRequest, "invalid_resource", "The request does not address a canonical path in this resource.", "Use the published route under the configured resource URL, without encoded separators or path traversal.")
	}
	if len(r.URL.RawQuery) > 4096 {
		return failure(http.StatusBadRequest, "invalid_parameters", "The request query exceeds the supported size limit.", "Use only the published bounded parameters, at most 4096 encoded query bytes.")
	}
	return 0, nil
}

func jsonHTTPFault(status int) *Fault {
	switch status {
	case http.StatusNotFound:
		return &Fault{Code: "unknown_route", Message: "No operation is registered at this path.", Action: "Use this resource's published HTTP paths.", Outcome: NotExecuted}
	case http.StatusMethodNotAllowed:
		return &Fault{Code: "method_not_allowed", Message: "This method is not supported for the selected route.", Action: "Use the method advertised in Allow and the published HTTP contract.", Outcome: NotExecuted}
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge:
		return sdkHTTPFault(status)
	default:
		return &Fault{Code: "invalid_request", Message: "The request could not be processed by this HTTP resource.", Action: "Check the published route and input contract; contact an authorized operator with the request ID if it persists.", Outcome: NotExecuted}
	}
}
