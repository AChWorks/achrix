// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// NewStreamingHTTP composes a private authenticated HTTP resource with explicit
// bounded file transfers. JSONHandler routes still buffer their JSON responses;
// only StreamHandler can deliver bytes directly. Credential verification,
// current admission, operation gates and owning Service authorization are the
// same as NewJSONHTTP. Construction starts no sessions or background work.
//
// The server/middleware must support actual socket read and write deadlines;
// otherwise requests fail before dispatch. Flushing is also required for
// successful file delivery; an unavailable or failed final flush preserves the
// known invocation outcome and is reported as a delivery failure. Limits bound total body bytes,
// response bytes, request duration and concurrency, not a whole-file buffer.
// Products must choose limits suitable for their file profile. This constructor
// adds no multipart parser, range support, public cache, SSE or URL fetcher.
func NewStreamingHTTP(config Config, handler http.Handler) (*Endpoint, error) {
	e, err := NewJSONHTTP(config, handler)
	if err != nil {
		return nil, err
	}
	e.streamHTTP = true
	return e, nil
}

// StreamHandler binds one explicit binary HTTP operation under NewStreamingHTTP.
// Decode validates cheap transport input before the operation gate. Invoke must
// call its ordinary authorized Service, honor context, return writer failures,
// and delay success headers until the content is known to be safe to deliver.
// It may set a successful status, content type/disposition and exact length.
// Request identity, no-store and nosniff headers remain boundary-owned.
//
// An error before any header is sent becomes a bounded safe Problem Details
// response. After headers, the endpoint aborts the transfer without appending
// JSON. Original writer/body causes and known outcomes remain protected. Invoke
// success (or Confirm) survives a later delivery failure. No retry is performed.
func StreamHandler[In any](operation string, decode func(*http.Request) (In, error), invoke func(context.Context, In, http.ResponseWriter) error) (http.Handler, error) {
	if !safeCode(operation) || decode == nil || invoke == nil {
		return nil, errors.New("bridge: a bounded fixed operation name, decoder and streaming invocation are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		input, s := beginHTTPInvocation(operation, w, r, decode, true)
		if s == nil {
			return
		}
		ctx, e, output := r.Context(), s.endpoint, s.stream
		err := invoke(ctx, input, output)
		phase := "invoke"
		if err == nil {
			s.mu.Lock()
			s.outcome = Succeeded
			s.mu.Unlock()
			if output.cause == nil {
				output.cause = output.finish()
			}
			if output.cause != nil {
				phase = "delivery"
				err = &Fault{Code: "response_delivery_failed", Message: "The operation succeeded but its file response could not be delivered completely.", Action: "Use the known outcome and authorized reference to reconcile; do not automatically repeat a write.", Outcome: Succeeded, HTTPStatus: http.StatusInternalServerError, Cause: output.cause}
			}
		}
		if err != nil {
			f, cause := normalizeFault(err, s.snapshot().Outcome)
			cause = httpInvocationCause(s, cause)
			if output.cause != nil && !errors.Is(cause, output.cause) {
				cause = errors.Join(cause, output.cause)
			}
			setInvocationFault(s, f, cause, phase)
			if output.started {
				output.abort = true
				e.report(ctx, s, phase, f.Code, cause, true)
				if phase != "delivery" && output.cause != nil {
					e.report(ctx, s, "delivery", "response_delivery_failed", output.cause, false)
				}
				return
			}
			e.failHTTP(w, r, s, httpStatus(f), phase, f, cause)
			return
		}
		e.report(ctx, s, "complete", "ok", nil, true)
	}), nil
}

// A raw upload can encounter an I/O failure after the owning Service has begun
// durable work. Retain that original cause without reclassifying its outcome as
// a pre-dispatch decoder failure or replacing the Service's safe guidance.
func httpInvocationCause(s *requestState, cause error) error {
	s.mu.Lock()
	bodyCause := s.bodyCause
	s.mu.Unlock()
	if bodyCause != nil && !errors.Is(cause, bodyCause) {
		return errors.Join(cause, bodyCause)
	}
	return cause
}

// streamResponse deliberately exposes neither Hijack/Unwrap nor Flush: an
// operation cannot escape the shared byte/deadline/outcome boundary. It holds
// headers and counters only, never a copy of the file. One invocation owns it.
type streamResponse struct {
	target  http.ResponseWriter
	header  http.Header
	ctx     context.Context
	limit   int64
	length  int64
	written int64
	status  int
	started bool
	abort   bool
	cause   error
}

func (w *streamResponse) Header() http.Header { return w.header }

func (w *streamResponse) WriteHeader(status int) {
	if w.status != 0 || w.started || w.cause != nil {
		return
	}
	if status < 200 || status >= 300 {
		w.cause = errors.New("stream handler must return a Fault instead of writing a non-success response")
		return
	}
	w.status = status // Success headers stay private until the first byte.
}

func (w *streamResponse) prepare() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(w.header.Get("Content-Type")), "text/event-stream") {
		return errors.New("SSE is outside the bounded file-transfer profile")
	}
	values := w.header.Values("Content-Length")
	if len(values) > 1 {
		return errors.New("ambiguous file response length")
	}
	if len(values) == 1 {
		length, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || length < 0 {
			return errors.New("invalid file response length")
		}
		if length > w.limit {
			return errResponseLimit
		}
		w.length = length
	}
	return nil
}

func (w *streamResponse) start() {
	for name, values := range w.header {
		if strings.EqualFold(name, "Mcp-Session-Id") || strings.EqualFold(name, "X-Request-ID") || strings.EqualFold(name, "Cache-Control") || strings.EqualFold(name, "X-Content-Type-Options") {
			continue
		}
		w.target.Header()[name] = append([]string(nil), values...)
	}
	if w.target.Header().Get("Content-Type") == "" {
		w.target.Header().Set("Content-Type", "application/octet-stream")
	}
	w.target.Header().Set("Cache-Control", "private, no-store")
	w.target.Header().Set("X-Content-Type-Options", "nosniff")
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.started = true
	w.target.WriteHeader(w.status)
}

func (w *streamResponse) Write(p []byte) (int, error) {
	if w.cause != nil {
		return 0, w.cause
	}
	if err := w.ctx.Err(); err != nil {
		w.cause = err
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil // No content means no success header has been committed.
	}
	if !w.started {
		if err := w.prepare(); err != nil {
			w.cause = err
			return 0, err
		}
	}
	if int64(len(p)) > w.limit-w.written {
		w.cause = errResponseLimit
		return 0, w.cause
	}
	if w.length >= 0 && int64(len(p)) > w.length-w.written {
		w.cause = http.ErrContentLength
		return 0, w.cause
	}
	if !w.started {
		w.start()
	}
	n, err := w.target.Write(p)
	w.written += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.cause = err
	return n, err
}

func (w *streamResponse) finish() error {
	if !w.started {
		if err := w.prepare(); err != nil {
			return err
		}
	}
	if w.length >= 0 && w.written != w.length {
		return io.ErrUnexpectedEOF
	}
	if !w.started {
		w.start()
	}
	// Flush the final buffered bytes while the delivery deadline remains live.
	// Server-owned protocol finalization still follows handler return, so the
	// outer endpoint deliberately leaves its deadlines installed through it.
	return http.NewResponseController(w.target).Flush()
}

var _ http.ResponseWriter = (*streamResponse)(nil)
