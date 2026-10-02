// SPDX-License-Identifier: MPL-2.0
// Package diagnosis owns the Notes diagnostic budget and trusted correlation.
package diagnosis

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
	"time"
)

type requestKey struct{}

func Correlate(ctx context.Context) (context.Context, string) {
	id := rand.Text()
	return context.WithValue(ctx, requestKey{}, id), id
}

// New composes an Info-level JSON handler with one runtime record per second per
// standard severity. Lifecycle/install records outside requests remain actionable.
// All derived loggers share the budget; no principal, IP, reason or request keys
// are retained. This controls diagnostic output, not admission or authorization.
func New(out io.Writer) *slog.Logger {
	return slog.New(handler{inner: slog.NewJSONHandler(out, nil), budget: &budget{now: time.Now}})
}

type handler struct {
	inner    slog.Handler
	budget   *budget
	frequent bool
}

func (h handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h handler) Handle(ctx context.Context, r slog.Record) error {
	id, request := ctx.Value(requestKey{}).(string)
	frequent := request || h.frequent
	r.Attrs(func(a slog.Attr) bool {
		frequent = frequent || repeatedOperation(a)
		return true
	})
	if frequent {
		suppressed, emit := h.budget.admit(r.Level)
		if !emit {
			return nil
		}
		if suppressed > 0 {
			// Counts cover all runtime records at this severity since its last
			// emitted record, not just this request, component or reason.
			r.AddAttrs(slog.Uint64("suppressed_records", suppressed))
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
	}
	h.inner = h.inner.WithAttrs(attrs)
	return h
}
func (h handler) WithGroup(group string) slog.Handler {
	h.inner = h.inner.WithGroup(group)
	return h
}

// Direct Application/Store calls share HTTP's bound. These operation values are
// source-owned categories; arbitrary operation strings never allocate new state.
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

type window struct {
	next       time.Time
	suppressed uint64
}
type budget struct {
	mu      sync.Mutex
	now     func() time.Time
	windows [4]window
}

func (b *budget) admit(level slog.Level) (uint64, bool) {
	index := 0
	switch {
	case level > slog.LevelWarn:
		index = 3
	case level > slog.LevelInfo:
		index = 2
	case level > slog.LevelDebug:
		index = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	w := &b.windows[index]
	now := b.now()
	if now.Before(w.next) {
		if w.suppressed < ^uint64(0) {
			w.suppressed++
		}
		return 0, false
	}
	suppressed := w.suppressed
	w.suppressed = 0
	w.next = now.Add(time.Second)
	return suppressed, true
}
