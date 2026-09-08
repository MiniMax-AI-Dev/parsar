package inbound

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestFeishuConnectionLogsOmitAuthenticationQuery(t *testing.T) {
	const address = "wss://frontier.example/ws/v2?device_id=device&access-key=synthetic-access&ticket=synthetic-ticket"
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	_, malformedErr := url.Parse("wss://frontier.example/ws/v2/%zz?extra=two words&access-key=synthetic-access&ticket=synthetic-ticket")
	if malformedErr == nil {
		t.Fatal("expected invalid URL escape")
	}
	for _, args := range [][]any{
		{"connected to", parsed, "[conn_id=connection-1]"},
		{"disconnected to " + address},
		{&url.Error{Op: "dial", URL: address, Err: errors.New("connection refused")}},
		{"connect failed: https://frontier.example/ws/v2?access%2Dkey=synthetic-access&ticket=synthetic-ticket"},
		{"retry " + address + " then " + address},
		{"connected to wss://frontier.example/ws/v2?extra='value'&access-key=synthetic-access&ticket=synthetic-ticket"},
		{&url.Error{Op: "dial", URL: `wss://frontier.example/ws/v2?extra="value"&access-key=synthetic-access&ticket=synthetic-ticket`, Err: errors.New("connection refused")}},
		{"connect failed:", malformedErr},
		{"connected to wss://frontier.example/ws/v2?extra=two\nwords&access-key=synthetic-access&ticket=synthetic-ticket"},
	} {
		sink := &sdkLogRecorder{}
		logger := feishuSDKLogger{logger: sink}
		logger.Info(t.Context(), args...)
		if len(sink.messages) != 1 {
			t.Fatalf("got %d log records", len(sink.messages))
		}
		message := sink.messages[0]
		if strings.Contains(message, "synthetic-access") || strings.Contains(message, "synthetic-ticket") {
			t.Fatalf("authentication query leaked: %s", message)
		}
		if !strings.Contains(message, "frontier.example/ws/v2") || !strings.Contains(message, "[REDACTED]") {
			t.Fatalf("connection diagnostics missing: %s", message)
		}
	}
	sink := &sdkLogRecorder{}
	feishuSDKLogger{logger: sink}.Info(t.Context(), "connected to "+address, "[conn_id=connection-1]")
	if got := sink.messages[0]; got != "connected to wss://frontier.example/ws/v2?[REDACTED] [conn_id=connection-1]" {
		t.Fatalf("SDK correlation field changed: %s", got)
	}
}

func TestFeishuSDKLoggerPreservesDiagnosticsAndLevels(t *testing.T) {
	sink := &sdkLogRecorder{}
	logger := feishuSDKLogger{logger: sink}
	logger.Debug(t.Context(), "debug payload remains disabled")
	logger.Info(t.Context(), "trying to reconnect: 2", "[conn_id=connection-1]")
	logger.Warn(t.Context(), "ping failed:", errors.New("connection is closed"))
	logger.Error(t.Context(), "handle message failed")
	want := []string{"trying to reconnect: 2 [conn_id=connection-1]", "ping failed: connection is closed", "handle message failed"}
	if len(sink.messages) != len(want) {
		t.Fatalf("records = %v", sink.messages)
	}
	for i, message := range want {
		if sink.messages[i] != message || sink.levels[i] != []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError}[i] {
			t.Fatalf("record %d = %s: %s", i, sink.levels[i], sink.messages[i])
		}
	}
}

type sdkLogRecorder struct {
	messages []string
	levels   []slog.Level
}

func (r *sdkLogRecorder) Log(_ context.Context, level slog.Level, message string, _ ...any) {
	r.messages = append(r.messages, message)
	r.levels = append(r.levels, level)
}
