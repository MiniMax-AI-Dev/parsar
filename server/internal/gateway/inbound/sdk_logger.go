package inbound

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// SDK fields may embed malformed URLs. Keep the operation and URL path, but
// omit the entire field tail after the query rather than guess its delimiter.
var feishuLogURLQuery = regexp.MustCompile(`(?is)(\b(?:https?|wss?)://[^?]*\?).*`)

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
	fields := make([]any, len(args))
	for i, arg := range args {
		fields[i] = redactFeishuConnectionLog(fmt.Sprint(arg))
	}
	message := strings.TrimSuffix(fmt.Sprintln(fields...), "\n")
	l.logger.Log(ctx, level, message)
}
