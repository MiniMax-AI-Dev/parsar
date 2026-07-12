package log

import (
	"context"
)

// traceCtxKey is an unexported type so external packages cannot
// collide on the same context key.
type traceCtxKey struct{}

// WithTrace returns ctx annotated with c. A zero-trace Carrier is a
// no-op so callers don't need a separate nil-check.
func WithTrace(ctx context.Context, c Carrier) context.Context {
	if c.Trace.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, traceCtxKey{}, c)
}

// TraceFromContext returns the Carrier attached by WithTrace, or
// (zero, false) when none is present.
func TraceFromContext(ctx context.Context) (Carrier, bool) {
	if ctx == nil {
		return Carrier{}, false
	}
	c, ok := ctx.Value(traceCtxKey{}).(Carrier)
	if !ok || c.Trace.IsZero() {
		return Carrier{}, false
	}
	return c, true
}
