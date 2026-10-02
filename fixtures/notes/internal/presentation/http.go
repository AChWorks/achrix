// SPDX-License-Identifier: MPL-2.0
package presentation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
}

func New(app *achrix.Application, s *application.Service, writer, reader string, l *slog.Logger) http.Handler {
	a := &Adapter{app: app, service: s, writer: writer, reader: reader, logger: l, slots: make(chan struct{}, 32)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"status": "live"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
		defer cancel()
		if err := app.Ready(ctx); err != nil {
			a.failure(w, r, 503, "not_ready")
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
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
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var input struct {
		Text string `json:"text"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		a.failure(w, r, 400, "invalid_input")
		return
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		a.failure(w, r, 400, "invalid_input")
		return
	}
	n, err := a.service.Create(r.Context(), p, input.Text)
	if err != nil {
		a.applicationError(w, r, err)
		return
	}
	reply(w, 201, n)
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
	reply(w, 200, n)
}
func (a *Adapter) applicationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, achrix.ErrDenied):
		a.failure(w, r, 403, "permission_denied")
	case errors.Is(err, domain.ErrInvalid):
		a.failure(w, r, 400, "invalid_input")
	case errors.Is(err, domain.ErrNotFound):
		a.failure(w, r, 404, "not_found")
	default:
		a.failure(w, r, 503, "unavailable")
	}
}
func (a *Adapter) failure(w http.ResponseWriter, r *http.Request, status int, code string) {
	level, message := slog.LevelInfo, "request rejected"
	if status >= 500 {
		level, message = slog.LevelWarn, "request failed"
	}
	a.logger.Log(r.Context(), level, message, "component", "notes.http", "operation", "request", "status", status, "reason", code)
	reply(w, status, map[string]string{"error": code})
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
