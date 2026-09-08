package inbound

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Connection query strings contain short-lived credentials. Redact only the
// log copy, retaining the host and path for connection diagnostics.
// RawQuery may contain quotes; they must not terminate redaction early.
var feishuLogURLQuery = regexp.MustCompile(`(?i)(\b(?:https?|wss?)://[^\s?]+\?)[^\s]+`)

func redactFeishuConnectionLog(message string) string {
	return feishuLogURLQuery.ReplaceAllString(message, "${1}[REDACTED]")
}

type feishuSDKLogger struct {
	logger interface {
		Log(context.Context, slog.Level, string, ...any)
	}
}

// The SDK's default minimum level is Info; keep payload debug logs disabled.
func (feishuSDKLogger) Debug(context.Context, ...any) {}

func (l feishuSDKLogger) Info(ctx context.Context, args ...any) {
	l.write(ctx, slog.LevelInfo, args...)
}

func (l feishuSDKLogger) Warn(ctx context.Context, args ...any) {
	l.write(ctx, slog.LevelWarn, args...)
}

func (l feishuSDKLogger) Error(ctx context.Context, args ...any) {
	l.write(ctx, slog.LevelError, args...)
}

func (l feishuSDKLogger) write(ctx context.Context, level slog.Level, args ...any) {
	message := strings.TrimSuffix(fmt.Sprintln(args...), "\n")
	l.logger.Log(ctx, level, redactFeishuConnectionLog(message))
}
