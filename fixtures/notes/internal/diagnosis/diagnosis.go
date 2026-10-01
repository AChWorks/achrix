// SPDX-License-Identifier: MPL-2.0
// Package diagnosis propagates consumer request correlation into ordinary slog.
package diagnosis

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
)

type requestKey struct{}

func Correlate(ctx context.Context) (context.Context, string) {
	id := rand.Text()
	return context.WithValue(ctx, requestKey{}, id), id
}

// Handler adds trusted generated correlation to Core, module and adapter records.
// No request payload, credentials or arbitrary error text is captured.
type handler struct{ inner slog.Handler }

func New(out io.Writer) *slog.Logger                             { return slog.New(handler{slog.NewJSONHandler(out, nil)}) }
func (h handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(requestKey{}).(string); ok {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.inner.Handle(ctx, r)
}
func (h handler) WithAttrs(a []slog.Attr) slog.Handler { return handler{h.inner.WithAttrs(a)} }
func (h handler) WithGroup(g string) slog.Handler      { return handler{h.inner.WithGroup(g)} }
