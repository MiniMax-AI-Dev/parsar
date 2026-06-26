package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestContextHandlerInjectsTrace(t *testing.T) {
	carrier, err := ParseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if err != nil {
		t.Fatalf("seed parse: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := WithTrace(context.Background(), carrier)
	logger.InfoContext(ctx, "hello", "k", "v")

	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, buf.String())
	}
	if got[AttrTraceID] != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("trace_id: %v", got[AttrTraceID])
	}
	if got[AttrSpanID] != "b7ad6b7169203331" {
		t.Fatalf("span_id: %v", got[AttrSpanID])
	}
	if got["k"] != "v" {
		t.Fatalf("user attr lost: %v", got["k"])
	}
}

// TestContextHandlerSkipsWhenNoTrace: no Carrier → no trace_id attr.
// Callers rely on "no trace_id field" to mean "outside a request".
func TestContextHandlerSkipsWhenNoTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	logger.InfoContext(context.Background(), "no trace here")
	if strings.Contains(buf.String(), AttrTraceID) {
		t.Fatalf("trace_id should be absent when ctx has no carrier; got %s", buf.String())
	}
}

// TestContextHandlerWithAttrsPreservesInjection: if WithAttrs unwraps
// our handler, trace injection silently stops the moment someone calls
// logger.With(...). Guard against that regression.
func TestContextHandlerWithAttrsPreservesInjection(t *testing.T) {
	carrier, _ := ParseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	var buf bytes.Buffer
	root := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	child := root.With("component", "test")
	child.InfoContext(WithTrace(context.Background(), carrier), "msg")
	out := buf.String()
	if !strings.Contains(out, "0af7651916cd43dd8448eb211c80319c") {
		t.Fatalf("trace_id missing after With(): %s", out)
	}
	if !strings.Contains(out, `"component":"test"`) {
		t.Fatalf("static attr missing: %s", out)
	}
}
