// SPDX-License-Identifier: MPL-2.0
package presentation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"github.com/AChWorks/achrix"
)

type Adapter struct {
	app            *achrix.Application
	service        *application.Service
	writer, reader string
	logger         *slog.Logger
	slots          chan struct{}
	diagnostic     func(context.Context, string, string, error)
}

func New(app *achrix.Application, s *application.Service, writer, reader string, l *slog.Logger) http.Handler {
	return NewWithDiagnostics(app, s, writer, reader, l, nil)
}

// NewWithDiagnostics preserves original transport failures at a protected
// product callback without exposing provider messages in HTTP or ordinary logs.
func NewWithDiagnostics(app *achrix.Application, s *application.Service, writer, reader string, l *slog.Logger, diagnostic func(context.Context, string, string, error)) http.Handler {
	if l == nil {
		l = slog.Default()
	}
	a := &Adapter{app: app, service: s, writer: writer, reader: reader, logger: l, slots: make(chan struct{}, 32), diagnostic: diagnostic}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, r *http.Request) {
		a.reply(w, r, http.StatusOK, map[string]string{"status": "live"}, "not_applicable")
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
		defer cancel()
		if err := app.Ready(ctx); err != nil {
			a.failure(w, r, 503, "not_ready")
			return
		}
		a.reply(w, r, 200, map[string]string{"status": "ready"}, "not_applicable")
	})
	mux.HandleFunc("POST /notes", a.create)
	mux.HandleFunc("GET /notes/{id}", a.read)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, id := diagnosis.Correlate(r.Context())
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
		default:
			a.failure(w, r, 503, "busy")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *Adapter) principal(r *http.Request) (achrix.Principal, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", achrix.ErrDenied
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.writer)) == 1 {
		return "writer", nil
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.reader)) == 1 {
		return "reader", nil
	}
	return "", achrix.ErrDenied
}
func (a *Adapter) create(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		a.failure(w, r, 401, "unauthenticated")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		a.failure(w, r, 415, "json_required")
		return
	}
	if err := r.Context().Err(); err != nil {
		a.decodeFailure(w, r, err, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	body := &bodyReader{reader: r.Body}
	var input struct {
		Text string `json:"text"`
	}
	d := json.NewDecoder(body)
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil || body.cause != nil {
		a.decodeFailure(w, r, err, body.cause)
		return
	}
	if err := d.Decode(&struct{}{}); err != io.EOF || body.cause != nil {
		a.decodeFailure(w, r, err, body.cause)
		return
	}
	n, err := a.service.Create(r.Context(), p, input.Text)
	if err != nil {
		if errors.Is(err, domain.ErrUnknownOutcome) {
			a.failureWith(w, r, 503, "unknown_outcome", map[string]string{"outcome": "unknown", "id": n.ID, "next_action": "reconcile this id and request with the operator before retrying"})
			return
		}
		a.applicationError(w, r, err)
		return
	}
	a.reply(w, r, 201, n, "committed")
}
func (a *Adapter) read(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		a.failure(w, r, 401, "unauthenticated")
		return
	}
	n, err := a.service.Read(r.Context(), p, r.PathValue("id"))
	if err != nil {
		a.applicationError(w, r, err)
		return
	}
	a.reply(w, r, 200, n, "not_applicable")
}

// Decoder errors do not reliably distinguish parser rejection from underlying
// I/O. Retain the original Read failure before the decoder can transform it.
type bodyReader struct {
	reader io.Reader
	cause  error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err != nil && err != io.EOF && b.cause == nil {
		b.cause = err
	}
	return n, err
}

func (a *Adapter) decodeFailure(w http.ResponseWriter, r *http.Request, err, readCause error) {
	var size *http.MaxBytesError
	if errors.As(err, &size) || errors.As(readCause, &size) {
		a.failureWith(w, r, http.StatusRequestEntityTooLarge, "input_too_large", map[string]string{"detail": "request body must contain at most 1024 bytes", "outcome": "not_executed"})
		return
	}
	if readCause != nil {
		a.diagnose(r.Context(), "decode_body", readCause, "not_executed")
		var timeout net.Error
		callerAborted := r.Context().Err() != nil && errors.Is(readCause, r.Context().Err())
		if callerAborted || errors.Is(readCause, context.DeadlineExceeded) || errors.As(readCause, &timeout) && timeout.Timeout() {
			a.failureWith(w, r, http.StatusRequestTimeout, "body_timeout", map[string]string{"outcome": "not_executed", "next_action": "send the complete JSON body within the request deadline"})
		} else {
			a.failureWith(w, r, http.StatusServiceUnavailable, "unavailable", map[string]string{"outcome": "not_executed", "next_action": "check the connection and service before sending a new request"})
		}
		return
	}
	a.failureWith(w, r, http.StatusBadRequest, "invalid_input", map[string]string{"detail": "send one JSON object with a text field", "outcome": "not_executed"})
}
func (a *Adapter) applicationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, achrix.ErrDenied):
		a.failure(w, r, 403, "permission_denied")
	case errors.Is(err, domain.ErrInvalid):
		details := map[string]string{}
		var input *domain.InputError
		if errors.As(err, &input) {
			details["field"], details["detail"] = input.Field, input.Problem
		}
		a.failureWith(w, r, 400, "invalid_input", details)
	case errors.Is(err, domain.ErrNotFound):
		a.failure(w, r, 404, "not_found")
	case errors.Is(err, domain.ErrNotExecuted):
		a.failureWith(w, r, 503, "unavailable", map[string]string{"outcome": "not_executed", "next_action": "check service readiness before trying again"})
	case errors.Is(err, domain.ErrNotCommitted):
		a.failureWith(w, r, 503, "unavailable", map[string]string{"outcome": "not_committed", "next_action": "ask the operator to resolve the rejected write before trying again"})
	case errors.Is(err, achrix.ErrAuthorizationUnavailable) && r.Method == http.MethodPost:
		a.failureWith(w, r, 503, "authorization_unavailable", map[string]string{"outcome": "not_executed", "next_action": "wait for authorization service recovery"})
	default:
		a.failure(w, r, 503, "unavailable")
	}
}
func (a *Adapter) failure(w http.ResponseWriter, r *http.Request, status int, code string) {
	a.failureWith(w, r, status, code, nil)
}
func (a *Adapter) failureWith(w http.ResponseWriter, r *http.Request, status int, code string, details map[string]string) {
	if details == nil {
		details = map[string]string{}
	}
	details["error"] = code
	if r.Method == http.MethodPost && details["outcome"] == "" && (status < 500 || code == "busy") {
		details["outcome"] = "not_executed"
	}
	level, message := slog.LevelInfo, "request rejected"
	if status >= 500 {
		level, message = slog.LevelWarn, "request failed"
	}
	a.logger.Log(r.Context(), level, message, "component", "notes.http", "operation", "request", "status", status, "reason", code, "outcome", details["outcome"])
	a.reply(w, r, status, details, details["outcome"])
}
func (a *Adapter) reply(w http.ResponseWriter, r *http.Request, status int, value any, outcome string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		a.diagnose(r.Context(), "write_response", err, outcome)
	}
}
func (a *Adapter) diagnose(ctx context.Context, phase string, cause error, outcome string) {
	if a.diagnostic != nil {
		a.diagnostic(ctx, "request", phase, cause)
	}
	level := slog.LevelError
	if outcome == "not_executed" && ctx.Err() != nil && errors.Is(cause, ctx.Err()) {
		level = slog.LevelDebug
	}
	a.logger.Log(ctx, level, "HTTP transport failed", "component", "notes.http", "operation", "request", "phase", phase, "reason", diagnosis.IOReason(cause), "outcome", outcome)
}
