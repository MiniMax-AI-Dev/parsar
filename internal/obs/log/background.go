package log

import (
	"context"
)

// StartBackgroundTrace returns a child ctx carrying a fresh Carrier
// so any log under it picks up a stable trace_id. Use at the top of
// every non-HTTP logical-request entrypoint (sweeper tick, WS envelope
// handler, CLI command). For a sub-step that should keep the trace,
// use ChildSpan instead.
func StartBackgroundTrace(parent context.Context, op string) (context.Context, Carrier) {
	if parent == nil {
		parent = context.Background()
	}
	c := NewCarrier()
	ctx := WithTrace(parent, c)
	if op != "" {
		_ = op
	}
	return ctx, c
}

// ChildSpan returns a ctx with the parent's trace_id and a fresh span_id.
// If the parent has no carrier, behaves like StartBackgroundTrace so
// callers don't have to branch on presence.
func ChildSpan(parent context.Context) (context.Context, Carrier) {
	if parent == nil {
		parent = context.Background()
	}
	if existing, ok := TraceFromContext(parent); ok {
		child := existing.ChildSpan()
		return WithTrace(parent, child), child
	}
	c := NewCarrier()
	return WithTrace(parent, c), c
}
