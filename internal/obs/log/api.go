// Direct slog.{Info,Warn,Error,Debug,Default} use outside this package
// is blocked by .golangci.yml forbidigo so the ctx-first signatures
// here are how trace_id auto-injection stays enforceable.
package log

import (
	"context"
	"log/slog"
)

// Info logs via slog.Default with ctx attached so ContextHandler can
// inject trace_id/span_id from ctx.
func Info(ctx context.Context, msg string, args ...any) {
	slog.Default().InfoContext(ctx, msg, args...)
}

// Bg returns slog.Default for ctx-less startup/init/shutdown sites.
// Using Bg() in any handler-path code is a bug — it bypasses trace
// attribution silently.
func Bg() *slog.Logger {
	return slog.Default()
}

// With binds attrs to a child logger that still routes through
// ContextHandler, so InfoContext etc. still pick up trace_id from ctx.
func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}
