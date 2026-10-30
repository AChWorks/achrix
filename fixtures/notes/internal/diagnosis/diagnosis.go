// SPDX-License-Identifier: MPL-2.0
// Package diagnosis owns the Notes diagnostic budget and trusted correlation.
package diagnosis

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

type requestKey struct{}

// WithRequestID joins a verified transport's server-generated request identity
// to Notes diagnostics. Callers must never supply a client header or payload ID.
// The transport owns the identity's validity and bounds; an empty ID generates
// the normal product-owned correlation instead.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		ctx, _ = Correlate(ctx)
		return ctx
	}
	return context.WithValue(ctx, requestKey{}, requestID)
}

// Correlate keeps an existing product-owned identity through nested adapters.
// Untrusted request headers never populate this private context key.
func Correlate(ctx context.Context) (context.Context, string) {
	if id, ok := ctx.Value(requestKey{}).(string); ok && id != "" {
		return ctx, id
	}
	id := rand.Text()
	return context.WithValue(ctx, requestKey{}, id), id
}

// New emits local JSON at Info. Its fixed budget retains first distinct runtime
// failures, then emits bounded suppression summaries even when traffic stops.
// This is a proving-product policy, not a mandatory Foundation logging policy.
func New(out io.Writer) *slog.Logger {
	inner := slog.NewJSONHandler(out, nil)
	return slog.New(handler{inner: inner, budget: &budget{inner: inner, now: time.Now, automatic: true}})
}

// Flush finishes pending summaries on orderly shutdown or a short-lived command.
// Normal runtime also flushes after one second without requiring another request.
func Flush(logger *slog.Logger) {
	if h, ok := logger.Handler().(handler); ok {
		h.budget.flush()
	}
}

type handler struct {
	inner    slog.Handler
	budget   *budget
	frequent bool
	fields   diagnosticFields
}

func (h handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h handler) Handle(ctx context.Context, r slog.Record) error {
	id, request := ctx.Value(requestKey{}).(string)
	fields := h.fields
	frequent, summary := request || h.frequent, false
	r.Attrs(func(a slog.Attr) bool {
		frequent = frequent || repeatedOperation(a)
		if a.Key == "diagnostic_summary" && a.Value.Kind() == slog.KindBool {
			summary = a.Value.Bool()
		}
		fields.add(a)
		return true
	})
	if frequent && !summary {
		pending, emit := h.budget.admit(r.Level, fields)
		h.budget.emit(pending)
		if !emit {
			return nil
		}
	}
	if request {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.inner.Handle(ctx, r)
}
func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		h.frequent = h.frequent || repeatedOperation(a)
		h.fields.add(a)
	}
	h.inner = h.inner.WithAttrs(attrs)
	return h
}
func (h handler) WithGroup(group string) slog.Handler { h.inner = h.inner.WithGroup(group); return h }

func repeatedOperation(a slog.Attr) bool {
	if a.Key != "operation" || a.Value.Kind() != slog.KindString {
		return false
	}
	switch a.Value.String() {
	case "authorize", "ready", "readiness", "insert", "select", "request", "example.notes.create", "example.notes.read":
		return true
	}
	return false
}

// Only bounded diagnostic classifications participate. Request IDs, resource IDs,
// messages and payloads are never retained or used as budget keys.
var fieldNames = [6]string{"component", "operation", "phase", "reason", "outcome", "cause_type"}

type diagnosticFields [6]string

func (f *diagnosticFields) add(a slog.Attr) {
	if a.Value.Kind() != slog.KindString {
		return
	}
	for i, name := range fieldNames {
		if a.Key == name {
			value := a.Value.String()
			if len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
				value = "other"
			}
			f[i] = value
			return
		}
	}
}
func (f diagnosticFields) attrs() []slog.Attr {
	attrs := make([]slog.Attr, 0, len(f))
	for i, value := range f {
		if value != "" {
			attrs = append(attrs, slog.String(fieldNames[i], value))
		}
	}
	return attrs
}

type sample struct {
	fields diagnosticFields
	count  uint64
	level  slog.Level
}
type window struct {
	samples  [32]sample
	used     int
	overflow uint64
}
type summary struct {
	sample
	overflow bool
}
type budget struct {
	mu         sync.Mutex
	emitMu     sync.Mutex
	now        func() time.Time
	inner      slog.Handler
	automatic  bool
	until      time.Time
	timer      *time.Timer
	generation uint64
	// Unknown mutation outcomes have their own capacity; readiness/authentication
	// floods cannot use it. All arrays have fixed size even under hostile input.
	windows [5]window
}

func (b *budget) admit(level slog.Level, fields diagnosticFields) ([]summary, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	var pending []summary
	if !b.until.IsZero() && !now.Before(b.until) {
		pending = b.takeLocked()
	}
	if b.until.IsZero() {
		b.until = now.Add(time.Second)
		b.generation++
		if b.automatic {
			generation := b.generation
			b.timer = time.AfterFunc(time.Second, func() { b.flushGeneration(generation) })
		}
	}
	index := 0
	switch {
	case fields[4] == "unknown":
		index = 4
	case level > slog.LevelWarn:
		index = 3
	case level > slog.LevelInfo:
		index = 2
	case level > slog.LevelDebug:
		index = 1
	}
	w := &b.windows[index]
	for i := 0; i < w.used; i++ {
		if w.samples[i].fields == fields {
			if w.samples[i].count < ^uint64(0) {
				w.samples[i].count++
			}
			return pending, false
		}
	}
	if w.used == len(w.samples) {
		if w.overflow < ^uint64(0) {
			w.overflow++
		}
		return pending, false
	}
	w.samples[w.used] = sample{fields: fields, level: level}
	w.used++
	return pending, true
}
func (b *budget) takeLocked() []summary {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	var result []summary
	for i := range b.windows {
		w := &b.windows[i]
		for j := 0; j < w.used; j++ {
			if w.samples[j].count > 0 {
				result = append(result, summary{sample: w.samples[j]})
			}
		}
		if w.overflow > 0 {
			result = append(result, summary{sample: sample{level: slog.LevelWarn, count: w.overflow}, overflow: true})
		}
		*w = window{}
	}
	b.until = time.Time{}
	return result
}
func (b *budget) emit(pending []summary) {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	b.emitLocked(pending)
}
func (b *budget) emitLocked(pending []summary) {
	for _, item := range pending {
		r := slog.NewRecord(b.now(), item.level, "repeated diagnostics suppressed", 0)
		r.AddAttrs(item.fields.attrs()...)
		r.AddAttrs(slog.Bool("diagnostic_summary", true), slog.Uint64("suppressed_records", item.count))
		if item.overflow {
			r.AddAttrs(slog.Bool("distinct_capacity_exceeded", true), slog.String("reason", "diagnostic_capacity"))
		}
		// A summary covers multiple requests; it must not borrow a request ID.
		_ = b.inner.Handle(context.Background(), r)
	}
}
func (b *budget) flush() { b.flushGeneration(0) }
func (b *budget) flushGeneration(generation uint64) {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	b.mu.Lock()
	if generation != 0 && generation != b.generation {
		b.mu.Unlock()
		return
	}
	pending := b.takeLocked()
	b.generation++
	b.mu.Unlock()
	b.emitLocked(pending)
}

// IOReason extracts operational facts without serializing addresses, paths or
// payloads carried by transport errors. The caller records its owned stage.
func IOReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, syscall.EADDRINUSE):
		return "address_in_use"
	case errors.Is(err, syscall.EACCES), errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.ENOSPC):
		return "no_space"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, io.ErrClosedPipe):
		return "closed_pipe"
	case errors.Is(err, syscall.ENOENT):
		return "file_missing"
	case errors.Is(err, syscall.EINVAL):
		return "invalid_argument"
	case errors.Is(err, io.ErrShortWrite):
		return "short_write"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed):
		return "closed"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "io_failure"
}
