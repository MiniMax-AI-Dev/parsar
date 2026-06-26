package log

import (
	"io"
	"log/slog"
)

// Discard returns a *slog.Logger that drops every record. Wrapped in
// ContextHandler so tests behave identically to production.
func Discard() *slog.Logger {
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})
	return slog.New(NewContextHandler(inner))
}
