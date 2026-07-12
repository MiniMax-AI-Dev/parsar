package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPMiddlewareAdoptsInboundHeader(t *testing.T) {
	const tp = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Info(r.Context(), "handler ran")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderName, tp)
	h.ServeHTTP(rec, req)

	if rec.Header().Get(HeaderName) != tp {
		t.Fatalf("response header echo: want %q got %q", tp, rec.Header().Get(HeaderName))
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, buf.String())
	}
	if got[AttrTraceID] != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("trace_id: %v", got[AttrTraceID])
	}
}

func TestHTTPMiddlewareMintsForMissingHeader(t *testing.T) {
	var observed string
	h := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := TraceFromContext(r.Context())
		if !ok {
			t.Fatalf("expected ctx to have a fresh carrier")
		}
		observed = c.String()
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	got := rec.Header().Get(HeaderName)
	if got == "" {
		t.Fatalf("missing response header")
	}
	if got != observed {
		t.Fatalf("response header differs from ctx carrier: header=%q ctx=%q", got, observed)
	}
}

// TestHTTPMiddlewareIgnoresMalformedHeader: bad header is treated as
// "no header" — never 500 a real request over a logging concern.
func TestHTTPMiddlewareIgnoresMalformedHeader(t *testing.T) {
	called := false
	h := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		c, _ := TraceFromContext(r.Context())
		if c.Trace.IsZero() {
			t.Fatalf("middleware should mint a carrier even with bad inbound header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderName, "garbage")
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("handler not invoked")
	}
}

func TestStartBackgroundTraceMintsFresh(t *testing.T) {
	ctx, c := StartBackgroundTrace(context.Background(), "test.op")
	if c.Trace.IsZero() {
		t.Fatalf("StartBackgroundTrace returned zero carrier")
	}
	got, ok := TraceFromContext(ctx)
	if !ok || got.Trace != c.Trace {
		t.Fatalf("ctx carrier mismatch: %+v want %+v", got, c)
	}
}

// TestChildSpanKeepsTraceRotatesSpan: child shares trace_id, has a
// different span_id.
func TestChildSpanKeepsTraceRotatesSpan(t *testing.T) {
	parentCtx, parent := StartBackgroundTrace(context.Background(), "")
	_, child := ChildSpan(parentCtx)
	if child.Trace != parent.Trace {
		t.Fatalf("child should keep trace; parent=%s child=%s",
			parent.Trace.String(), child.Trace.String())
	}
	if child.Span == parent.Span {
		t.Fatalf("child should rotate span; both=%s", child.Span.String())
	}
}
