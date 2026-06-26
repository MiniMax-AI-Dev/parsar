package log

import (
	"context"
	"log/slog"
)

// AttrTraceID / AttrSpanID are slog attr keys for the W3C IDs. Stable
// snake_case so log scrapers can build dashboards on a fixed name.
const (
	AttrTraceID = "trace_id"
	AttrSpanID  = "span_id"
)

// ContextHandler wraps a slog.Handler and injects trace_id/span_id
// from the record's ctx. Records with no Carrier pass through
// unchanged so background tasks don't get fake all-zero IDs.
type ContextHandler struct {
	inner slog.Handler
}

func NewContextHandler(inner slog.Handler) *ContextHandler {
	if inner == nil {
		inner = slog.Default().Handler()
	}
	return &ContextHandler{inner: inner}
}

func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if carrier, ok := TraceFromContext(ctx); ok {
		r.AddAttrs(
			slog.String(AttrTraceID, carrier.Trace.String()),
			slog.String(AttrSpanID, carrier.Span.String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs / WithGroup MUST re-wrap so slog.Default().With(...) does
// not unwrap us and silently lose trace_id injection.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{inner: h.inner.WithGroup(name)}
}
