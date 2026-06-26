package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Config drives Init.
type Config struct {
	// Format is "json" or "text". Empty auto-detects: text on a TTY,
	// JSON otherwise.
	Format string
	// Level is the minimum slog level. Empty defaults to Info.
	Level slog.Level
	// AddSource toggles slog's filename:line attribute (~hundreds of
	// ns per line — fine in dev, costly in prod).
	AddSource bool
	// Out is the destination writer. Nil defaults to os.Stderr.
	Out io.Writer
}

// ConfigFromEnv reads:
//
//	PARSAR_LOG_FORMAT     = json | text  (default: auto)
//	PARSAR_LOG_LEVEL      = debug | info | warn | error  (default: info)
//	PARSAR_LOG_ADD_SOURCE = 0 | 1  (default: 0)
//
// Unknown values fall back to defaults — Init runs before most
// error-handling exists, so "boot anyway" beats "panic on typo".
func ConfigFromEnv() Config {
	cfg := Config{
		Format:    strings.ToLower(strings.TrimSpace(os.Getenv("PARSAR_LOG_FORMAT"))),
		Level:     parseLevel(os.Getenv("PARSAR_LOG_LEVEL")),
		AddSource: os.Getenv("PARSAR_LOG_ADD_SOURCE") == "1",
		Out:       os.Stderr,
	}
	return cfg
}

// isTerminal reports whether f is a character device (TTY) so the JSON
// vs. text auto-detect doesn't need the golang.org/x/term dep.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// initOnce guarantees Init's slog.SetDefault side-effect runs at most
// once per process so tests don't fight over the global handler.
var initOnce sync.Once

// Init installs ContextHandler as slog.Default. Calling more than once
// is a no-op — only one global slog handler exists.
func Init(cfg Config) {
	initOnce.Do(func() {
		slog.SetDefault(buildLogger(cfg))
	})
}

// buildLogger is split out so tests can build a logger without
// triggering the global SetDefault side-effect.
func buildLogger(cfg Config) *slog.Logger {
	out := cfg.Out
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}
	format := cfg.Format
	if format == "" {
		format = "json"
		if f, ok := out.(*os.File); ok && isTerminal(f) {
			format = "text"
		}
	}
	var inner slog.Handler
	switch format {
	case "text":
		inner = slog.NewTextHandler(out, opts)
	default:
		inner = slog.NewJSONHandler(out, opts)
	}
	return slog.New(NewContextHandler(inner))
}
