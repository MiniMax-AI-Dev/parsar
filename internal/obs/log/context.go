package log

import (
	"context"
	"log/slog"
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

// Ctx returns a ctxLogger so `log.Ctx(ctx).Info(...)` reads cleaner
// than `slog.Default().InfoContext(ctx, ...)`. ctxLogger is stateless
// w.r.t. trace IDs — every call re-reads ctx so a child span minted
// via StartBackgroundTrace is not cached as a stale carrier.
func Ctx(ctx context.Context) ctxLogger { return ctxLogger{ctx: ctx} }

type ctxLogger struct {
	ctx context.Context
}

func (l ctxLogger) Debug(msg string, args ...any) {
	slog.Default().DebugContext(l.ctx, msg, args...)
}

func (l ctxLogger) Info(msg string, args ...any) {
	slog.Default().InfoContext(l.ctx, msg, args...)
}

func (l ctxLogger) Warn(msg string, args ...any) {
	slog.Default().WarnContext(l.ctx, msg, args...)
}

func (l ctxLogger) Error(msg string, args ...any) {
	slog.Default().ErrorContext(l.ctx, msg, args...)
}

// With returns a slog.Logger with the supplied attrs bound. Ctx-derived
// trace attrs still apply on subsequent InfoContext calls.
func (l ctxLogger) With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}
