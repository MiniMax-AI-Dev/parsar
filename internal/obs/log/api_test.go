package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestInfoEmitsTraceID(t *testing.T) {
	carrier, _ := ParseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(prev) })

	Info(WithTrace(context.Background(), carrier), "hello", "k", "v")
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, buf.String())
	}
	if got[AttrTraceID] != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("trace_id: %v", got[AttrTraceID])
	}
	if got["k"] != "v" {
		t.Fatalf("user attr lost: %v", got["k"])
	}
}

// TestBgOmitsTraceID: Bg().Info must not emit trace_id — that's how
// "log line came from outside a request" stays distinguishable.
func TestBgOmitsTraceID(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(prev) })

	Bg().Info("startup")
	if strings.Contains(buf.String(), AttrTraceID) {
		t.Fatalf("Bg().Info should NOT emit trace_id; got %s", buf.String())
	}
}

func TestWithBindsAttrsAndPreservesTrace(t *testing.T) {
	carrier, _ := ParseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logger := With("component", "test")
	logger.InfoContext(WithTrace(context.Background(), carrier), "msg")

	out := buf.String()
	if !strings.Contains(out, `"component":"test"`) {
		t.Fatalf("static attr missing: %s", out)
	}
	if !strings.Contains(out, "0af7651916cd43dd8448eb211c80319c") {
		t.Fatalf("trace_id missing on logger built by With(): %s", out)
	}
}
