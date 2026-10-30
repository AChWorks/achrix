// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errResponseLimit = errors.New("bridge response exceeded the configured byte limit")

// ServeHTTP handles the endpoint's bounded MCP or JSON HTTP profile. Each request
// verifies the credential and current admission; no connection principal or
// business permission is cached. No request or operation is automatically replayed.
func (e *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), e.config.Limits.Timeout)
	defer cancel()
	channel := "mcp"
	if e.jsonHTTP {
		channel = "http"
	}
	s := &requestState{endpoint: e, request: Request{ID: rand.Text(), Channel: channel}, outcome: NotExecuted}
	ctx = context.WithValue(ctx, requestKey{}, s)
	s.ctx = ctx
	r = r.WithContext(ctx)
	w.Header().Set("X-Request-ID", s.request.ID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Keep a separate finite delivery allowance, so a compliant operation
	// ending at its deadline can still return a useful truthful timeout result.
	deadline, _ := ctx.Deadline()
	controller := http.NewResponseController(w)
	readDeadlineErr := controller.SetReadDeadline(deadline)
	writeDeadlineErr := controller.SetWriteDeadline(deadline.Add(time.Second))
	if !e.streamHTTP {
		defer controller.SetReadDeadline(time.Time{})
		defer controller.SetWriteDeadline(time.Time{})
	}
	// The file profile must retain both deadlines through net/http's final
	// buffered writes/framing and unread-body close after ServeHTTP returns.
	// The server resets HTTP/1 connection deadlines before reuse and cleans
	// HTTP/2 stream timers on close; clearing them here would unbound that tail.
	if e.streamHTTP {
		if err := errors.Join(readDeadlineErr, writeDeadlineErr); err != nil {
			f := &Fault{Code: "transport_unavailable", Message: "This HTTP transport cannot enforce the required file-transfer deadlines.", Action: "Use a server and middleware that support bounded socket read and write deadlines. No operation was invoked.", Outcome: NotExecuted}
			e.failHTTP(w, r, s, http.StatusServiceUnavailable, "transport", f, err)
			return
		}
		s.stream = &streamResponse{target: w, header: make(http.Header), ctx: ctx, limit: e.config.Limits.ResponseBytes, length: -1}
	}
	if status, f := e.validateHTTP(r); f != nil {
		e.failHTTP(w, r, s, status, "transport", f, errors.New(f.Message))
		return
	}
	if !e.jsonHTTP && r.Header.Get("Mcp-Protocol-Version") == "" {
		// The SDK otherwise defaults to 2025-03-26, which still permits batch
		// arrays. This adapter has one invocation state per POST. Select the
		// supported legacy profile so SDK rejects batches before dispatch,
		// while retaining headerless legacy initialization. Clone the headers
		// rather than mutating the surrounding HTTP middleware's request.
		r = r.Clone(ctx)
		r.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	}
	select {
	case e.active <- struct{}{}:
		defer func() { <-e.active }()
	default:
		e.failHTTP(w, r, s, http.StatusServiceUnavailable, "admission", &Fault{Code: "busy", Message: "The endpoint is handling its maximum concurrent requests.", Action: "Wait before submitting a new request. This request did not invoke an operation.", Outcome: NotExecuted}, errors.New("bridge concurrency limit reached"))
		return
	}
	// Keep the exact body I/O failure before the SDK converts it to HTTP text.
	if r.Body != nil {
		if e.jsonHTTP {
			r.Body = http.MaxBytesReader(w, r.Body, e.config.Limits.RequestBytes)
		}
		r.Body = &recordingBody{ReadCloser: r.Body, state: s}
	}
	response := &boundedResponse{header: make(http.Header), limit: e.config.Limits.ResponseBytes}
	e.handler.ServeHTTP(response, r)
	if s.stream != nil && s.stream.started {
		if s.stream.abort {
			// A partial attachment is never followed by a JSON problem or a
			// second response. net/http closes HTTP/1 or resets the HTTP/2 stream.
			panic(http.ErrAbortHandler)
		}
		return
	}
	if response.overflow {
		f := &Fault{Code: "response_too_large", Message: "The operation response exceeded the endpoint's size limit.", Action: "Use the known outcome and reference to reconcile any write; reduce a read's requested result size before trying again.", Outcome: s.snapshot().Outcome}
		e.report(ctx, s, "output", f.Code, errResponseLimit, false)
		e.writeProblem(w, r, s, http.StatusInternalServerError, f)
		return
	}
	if response.status == 0 {
		response.status = http.StatusOK
	}
	if response.status >= 400 {
		// The SDK bearer middleware deliberately emits only safe verifier errors,
		// but its plain HTTP responses still need useful code/action/correlation.
		if response.header.Get("Content-Type") != "application/problem+json" {
			s.mu.Lock()
			ownFault := s.fault
			ownCause := s.cause
			ownPhase := s.phase
			s.mu.Unlock()
			status := response.status
			f := ownFault
			if f == nil {
				if e.jsonHTTP {
					f = jsonHTTPFault(status)
				} else {
					f = sdkHTTPFault(status)
				}
			}
			if ownPhase == "authentication" && status >= 500 {
				status = http.StatusServiceUnavailable
			}
			if challenge := response.header.Get("WWW-Authenticate"); challenge != "" {
				w.Header().Set("WWW-Authenticate", challenge)
			}
			if allow := response.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			if ownCause == nil {
				s.mu.Lock()
				ownCause = s.bodyCause
				s.mu.Unlock()
				if ownCause != nil {
					ownPhase = "body_read"
					f = &Fault{Code: "body_unavailable", Message: "The request body could not be read completely.", Action: "Check the connection and request deadline; no operation was invoked if the outcome is not_executed.", Outcome: s.snapshot().Outcome}
					var timeout net.Error
					if errors.As(ownCause, &timeout) && timeout.Timeout() || errors.Is(ownCause, context.DeadlineExceeded) {
						status = http.StatusRequestTimeout
						f = &Fault{Code: "body_timeout", Message: "The request body did not arrive within the request deadline.", Action: "Send the complete bounded body within the deadline. No operation was invoked.", Outcome: NotExecuted}
					}
				} else {
					ownCause = errors.New(response.body.String())
					ownPhase = "protocol"
					if !e.jsonHTTP {
						if wire := decodeRPCError(response.body.Bytes()); wire != nil {
							ownCause = wire
							f = protocolFault(wire)
						}
					}
				}
			}
			if ctx.Err() != nil && ownPhase != "body_read" {
				f = timeoutFault(s.snapshot().Outcome)
				status = http.StatusServiceUnavailable
			}
			e.report(ctx, s, ownPhase, f.Code, ownCause, true)
			e.writeProblem(w, r, s, status, f)
			return
		}
	}
	if strings.HasPrefix(response.header.Get("Content-Type"), "text/event-stream") {
		f := &Fault{Code: "unsupported_method", Message: "Streaming subscriptions are not supported by this endpoint.", Action: "Use the registered request-only tools.", Outcome: NotExecuted}
		e.report(ctx, s, "protocol", f.Code, errors.New("SDK attempted an SSE response outside the JSON profile"), true)
		e.writeProblem(w, r, s, http.StatusBadRequest, f)
		return
	}
	// SDK parameter decoding can fail before receiving middleware is called.
	// Preserve the protocol code and original typed error while projecting
	// only safe correction details. Use the SDK's JSON-RPC codec, not a second
	// envelope parser or dispatcher.
	if !e.jsonHTTP {
		if message, err := jsonrpc.DecodeMessage(response.body.Bytes()); err == nil {
			if rpc, ok := message.(*jsonrpc.Response); ok && rpc.Error != nil {
				s.mu.Lock()
				reported := s.reported
				s.mu.Unlock()
				if !reported {
					var wire *jsonrpc.Error
					if !errors.As(rpc.Error, &wire) {
						wire = &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "MCP response construction failed"}
					}
					f := protocolFault(wire)
					phase := "protocol"
					if s.snapshot().Dispatched {
						phase = "output"
						f = &Fault{Code: "output_unavailable", Message: "The operation ran but its response could not be produced.", Action: "Reconcile the known outcome and reference before repeating a write.", Outcome: s.snapshot().Outcome}
					}
					e.report(ctx, s, phase, f.Code, rpc.Error, true)
					data, _ := json.Marshal(project(f, s))
					rpc.Error = &jsonrpc.Error{Code: wire.Code, Message: f.Message, Data: data}
					encoded, encodeErr := jsonrpc.EncodeMessage(rpc)
					if encodeErr == nil {
						response.body.Reset()
						_, _ = response.Write(encoded)
					}
				}
			}
		}
	}
	if response.body.Len() == 0 && ctx.Err() != nil {
		e.report(ctx, s, "delivery", "deadline_exceeded", ctx.Err(), false)
		e.writeProblem(w, r, s, http.StatusServiceUnavailable, timeoutFault(s.snapshot().Outcome))
		return
	}
	if response.overflow {
		f := &Fault{Code: "response_too_large", Message: "The response exceeded the endpoint's size limit.", Action: "Reconcile any write using its known outcome and reference before trying again.", Outcome: s.snapshot().Outcome}
		e.report(ctx, s, "output", f.Code, errResponseLimit, false)
		e.writeProblem(w, r, s, http.StatusInternalServerError, f)
		return
	}
	for name, values := range response.header {
		if strings.EqualFold(name, "Mcp-Session-Id") || strings.EqualFold(name, "X-Request-ID") || strings.EqualFold(name, "Cache-Control") {
			continue
		}
		w.Header()[name] = append([]string(nil), values...)
	}
	w.WriteHeader(response.status)
	if err := writeAll(w, response.body.Bytes()); err != nil {
		e.report(ctx, s, "delivery", "response_delivery_failed", err, false)
	}
}

func (e *Endpoint) validateHTTP(r *http.Request) (int, *Fault) {
	failure := func(status int, code, message, action string) (int, *Fault) {
		return status, &Fault{Code: code, Message: message, Action: action, Outcome: NotExecuted}
	}
	if !strings.EqualFold(r.Host, e.resource.Host) {
		return failure(http.StatusForbidden, "invalid_host", "The request host does not match this endpoint.", "Use the configured canonical resource URL.")
	}
	for _, name := range []string{"Authorization", "Origin", "Sec-Fetch-Site", "Content-Type", "Mcp-Protocol-Version", "Mcp-Method", "Mcp-Name", "Mcp-Session-Id"} {
		if len(r.Header.Values(name)) > 1 {
			return failure(http.StatusBadRequest, "ambiguous_header", "A security-relevant request header appears more than once.", "Send one value for each authorization and protocol control header.")
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" && !e.origins[origin] {
		return failure(http.StatusForbidden, "origin_denied", "This request origin is not permitted.", "Use an explicitly admitted product origin or a supported non-browser client.")
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" && r.Header.Get("Origin") == "" {
		return failure(http.StatusForbidden, "origin_denied", "A cross-site browser request lacks an admitted origin.", "Use an explicitly admitted product origin.")
	}
	if r.ContentLength > e.config.Limits.RequestBytes {
		return failure(http.StatusRequestEntityTooLarge, "request_too_large", "The request exceeds the endpoint's input limit.", "Reduce the request to the operation's bounded input contract.")
	}
	if len(r.Header.Get("Authorization")) > 8192 {
		return failure(http.StatusUnauthorized, "invalid_credential", "The supplied credential cannot be accepted.", "Obtain a current credential for this resource and send it in one Bearer authorization header.")
	}
	if e.jsonHTTP {
		return e.validateJSONHTTP(r)
	}
	path := e.resource.Path
	if path == "" {
		path = "/"
	}
	if r.URL.Path != path || r.URL.RawPath != "" || r.URL.RawQuery != "" {
		return failure(http.StatusBadRequest, "invalid_resource", "The request does not address the configured resource.", "Use the canonical resource URL without query parameters.")
	}
	if r.Method != http.MethodPost {
		return failure(http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts request-only MCP POSTs.", "Use POST with JSON; listening, sessions and subscriptions are not enabled.")
	}
	if r.Header.Get("Mcp-Session-Id") != "" {
		return failure(http.StatusBadRequest, "sessions_unsupported", "This endpoint does not retain MCP sessions.", "Send each authenticated request without a session identifier.")
	}
	if r.Header.Get("Mcp-Method") == "subscriptions/listen" {
		return failure(http.StatusBadRequest, "unsupported_method", "Subscriptions are not enabled.", "Use the registered request-only tools.")
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return failure(http.StatusUnsupportedMediaType, "unsupported_content_type", "MCP requests must use application/json.", "Send a JSON request with Content-Type: application/json.")
	}
	if version := r.Header.Get("Mcp-Protocol-Version"); version != "" && version != "2026-07-28" && version != "2025-11-25" {
		return failure(http.StatusBadRequest, "unsupported_protocol", "This MCP protocol version is not enabled.", "Use protocol 2026-07-28 or 2025-11-25.")
	}
	return 0, nil
}

func (e *Endpoint) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	s := stateFromContext(ctx)
	var credential Credential
	err := ctx.Err()
	if err == nil {
		credential, err = e.config.Verify(ctx, token)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && (credential.Caller.Principal == "" || credential.Caller.ClientID == "" || !boundedText(string(credential.Caller.Principal), 512) || !boundedText(credential.Caller.ClientID, 256) || !boundedText(credential.Caller.ConnectionID, 256) || !validScopes(credential.Scopes)) {
		err = errors.New("credential verifier returned an invalid user/client/scope binding")
	}
	if err == nil && (credential.ExpiresAt.IsZero() || !time.Now().Before(credential.ExpiresAt)) {
		err = ErrInvalidCredential
	}
	if err != nil {
		f := &Fault{Code: "verification_unavailable", Message: "Credential verification is temporarily unavailable.", Action: "Contact an authorized operator with the request ID. No operation was invoked.", Outcome: NotExecuted}
		publicErr := errors.New("credential verification unavailable")
		if errors.Is(err, ErrInvalidCredential) {
			f = &Fault{Code: "invalid_credential", Message: "The credential is invalid, expired, revoked, or not valid for this resource.", Action: "Reconnect using a current credential issued for this resource.", Outcome: NotExecuted}
			publicErr = auth.ErrInvalidToken
		}
		setInvocationFault(s, f, err, "authentication")
		e.report(ctx, s, "authentication", f.Code, err, true)
		return nil, publicErr
	}
	credential.Scopes = append([]string(nil), credential.Scopes...)
	s.mu.Lock()
	s.request.Caller = credential.Caller
	s.mu.Unlock()
	for _, scope := range e.config.Scopes {
		if !slices.Contains(credential.Scopes, scope) {
			f := &Fault{Code: "insufficient_scope", Message: "The connection grant does not include the required scope.", Action: "Reconnect with the scope advertised by this resource; product permissions still apply.", Outcome: NotExecuted}
			setInvocationFault(s, f, errors.New("required transport scope is absent"), "authentication")
			break // SDK performs the standard 403 scope challenge.
		}
	}
	return &auth.TokenInfo{Scopes: credential.Scopes, Expiration: credential.ExpiresAt, UserID: string(credential.Caller.Principal), Extra: map[string]any{"achrix.bridge.request": s}}, nil
}

func (e *Endpoint) failHTTP(w http.ResponseWriter, r *http.Request, s *requestState, status int, phase string, f *Fault, cause error) {
	e.report(r.Context(), s, phase, f.Code, cause, true)
	e.writeProblem(w, r, s, status, f)
}

func (e *Endpoint) writeProblem(w http.ResponseWriter, r *http.Request, s *requestState, status int, f *Fault) {
	p := project(f, s)
	data := encodeProblem(status, p)
	if int64(len(data)) > e.config.Limits.ResponseBytes {
		e.report(r.Context(), s, "output", "response_too_large", fmt.Errorf("public problem %s encoded to %d bytes with limit %d: %w", p.Code, len(data), e.config.Limits.ResponseBytes, errResponseLimit), false)
		status = http.StatusInternalServerError
		p = publicFault{
			Code:    "response_too_large",
			Message: "The response exceeded the configured size limit.",
			Action:  "Use the known outcome and complete reference to reconcile any write; contact an operator with the request ID.",
			Outcome: p.Outcome, Reference: p.Reference, RequestID: p.RequestID,
		}
		// A 256-byte valid reference needs at most 1536 JSON-escaped bytes.
		// Fixed metadata, all outcome names and the generated correlation ID
		// consume less than 1 KiB. The 4096-byte minimum therefore fits this
		// fallback without shortening a reference or removing its evidence.
		data = encodeProblem(status, p)
		w.Header().Del("WWW-Authenticate")
		w.Header().Del("Allow")
	}
	if status == http.StatusMethodNotAllowed && !e.jsonHTTP {
		w.Header().Set("Allow", http.MethodPost)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden && p.Code == "insufficient_scope" {
		challenge := fmt.Sprintf("Bearer resource_metadata=%q", e.metadataURL)
		if len(e.config.Scopes) > 0 {
			challenge += fmt.Sprintf(", scope=%q", strings.Join(e.config.Scopes, " "))
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Request-ID", p.RequestID)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := writeAll(w, data); err != nil {
		e.report(r.Context(), s, "delivery", "response_delivery_failed", err, false)
	}
}

func encodeProblem(status int, projection publicFault) []byte {
	body := struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
		publicFault
	}{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: projection.Message, publicFault: projection}
	data, _ := json.Marshal(body)
	return data
}

func writeAll(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func decodeRPCError(data []byte) *jsonrpc.Error {
	message, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok {
		return nil
	}
	var wire *jsonrpc.Error
	if errors.As(response.Error, &wire) {
		return wire
	}
	return nil
}

func protocolFault(wire *jsonrpc.Error) *Fault {
	f := &Fault{Code: "invalid_request", Message: "The MCP request does not satisfy the protocol contract.", Action: "Correct the request envelope and parameters for the supported protocol.", Fields: map[string]string{"jsonrpc_code": fmt.Sprint(wire.Code)}, Outcome: NotExecuted}
	switch wire.Code {
	case mcp.CodeHeaderMismatch:
		f.Code = "header_mismatch"
		f.Message = "MCP control headers are missing or disagree with the request body."
		f.Action = "Match Mcp-Method, Mcp-Name and Mcp-Protocol-Version to the JSON method, tool name and _meta.protocolVersion."
	case mcp.CodeUnsupportedProtocolVersion:
		f.Code = "unsupported_protocol"
		f.Action = "Use protocol 2026-07-28 or 2025-11-25."
	case jsonrpc.CodeParseError:
		f.Code = "invalid_json"
		f.Action = "Send one syntactically valid JSON-RPC object."
	case jsonrpc.CodeInvalidParams:
		f.Code = "invalid_parameters"
		f.Action = "Check the method's required parameters and the tool's published input schema."
	case jsonrpc.CodeMethodNotFound:
		f.Code = "unsupported_method"
		f.Action = "Use discovery and the registered request-only tools."
	}
	return f
}

type recordingBody struct {
	io.ReadCloser
	state *requestState
}

func (b *recordingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.state.mu.Lock()
		if b.state.bodyCause == nil {
			b.state.bodyCause = err
		}
		b.state.mu.Unlock()
	}
	return n, err
}

func sdkHTTPFault(status int) *Fault {
	switch status {
	case http.StatusUnauthorized:
		return &Fault{Code: "invalid_credential", Message: "A current Bearer credential is required.", Action: "Connect to the advertised authorization server for this resource.", Outcome: NotExecuted}
	case http.StatusForbidden:
		return &Fault{Code: "insufficient_scope", Message: "The connection is not authorized for this request.", Action: "Check the requested resource, grant scope and current connection admission.", Outcome: NotExecuted}
	case http.StatusRequestEntityTooLarge:
		return &Fault{Code: "request_too_large", Message: "The request exceeds the endpoint's input limit.", Action: "Reduce the input to the tool's bounded contract.", Outcome: NotExecuted}
	case http.StatusUnsupportedMediaType:
		return &Fault{Code: "unsupported_content_type", Message: "The request must contain JSON.", Action: "Set Content-Type to application/json.", Outcome: NotExecuted}
	default:
		return &Fault{Code: "invalid_request", Message: "The request does not satisfy the single-request MCP HTTP contract.", Action: "Send one JSON-RPC object per POST; check the supported protocol version, Accept headers and matching MCP control headers.", Outcome: NotExecuted}
	}
}

type boundedResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	limit    int64
	overflow bool
}

func (w *boundedResponse) Header() http.Header { return w.header }
func (w *boundedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *boundedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow || int64(w.body.Len())+int64(len(p)) > w.limit {
		w.overflow = true
		return 0, errResponseLimit
	}
	return w.body.Write(p)
}

// Flush does not deliver partial responses. Streaming is outside this profile.
func (w *boundedResponse) Flush() {}

var _ http.ResponseWriter = (*boundedResponse)(nil)
var _ http.Flusher = (*boundedResponse)(nil)
var _ io.Writer = (*boundedResponse)(nil)
