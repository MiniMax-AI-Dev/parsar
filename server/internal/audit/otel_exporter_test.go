package audit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

// TestOTelExporterSinkRejectsEmptyEndpoint — a misconfigured fan-out
// target must fail loudly instead of silently swallowing every write.
func TestOTelExporterSinkRejectsEmptyEndpoint(t *testing.T) {
	for _, ep := range []string{"", "   "} {
		if _, err := NewOTelExporterSink(OTelExporterOptions{Endpoint: ep}); err == nil {
			t.Errorf("NewOTelExporterSink(%q) should error", ep)
		}
	}
}

// TestOTelExporterSinkNormalizesEndpoint — bare host:port, trailing
// slash, and full /v1/logs URL must all land on the same `/v1/logs`
// POST target; the doubled-path form (`/v1/logs/v1/logs`) produces
// 404s that are hard to debug from the receiver-side error alone.
func TestOTelExporterSinkNormalizesEndpoint(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"base url", "https://otel.example.com:4318"},
		{"base url trailing slash", "https://otel.example.com:4318/"},
		{"full logs url", "https://otel.example.com:4318/v1/logs"},
		{"full logs url trailing slash", "https://otel.example.com:4318/v1/logs/"},
	}
	const want = "https://otel.example.com:4318/v1/logs"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink, err := NewOTelExporterSink(OTelExporterOptions{Endpoint: tc.in})
			if err != nil {
				t.Fatalf("NewOTelExporterSink: %v", err)
			}
			if sink.endpoint != want {
				t.Errorf("endpoint = %q; want %q", sink.endpoint, want)
			}
		})
	}
}

// TestOTelExporterSinkPostsValidOTLPLog asserts an Event serializes into
// a parseable OTLP/HTTP log payload. The test decodes the body with the
// same proto types the receiver package would, proving wire compat.
func TestOTelExporterSinkPostsValidOTLPLog(t *testing.T) {
	var (
		mu       sync.Mutex
		captured []byte
		captHdrs http.Header
		captPath string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = body
		captHdrs = r.Header.Clone()
		captPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, err := NewOTelExporterSink(OTelExporterOptions{
		Endpoint:   srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOTelExporterSink: %v", err)
	}

	ev := Event{
		OccurredAt:  time.Unix(1_700_000_000, 0).UTC(),
		Source:      SourceAdmin,
		EventType:   "model.created",
		ActorType:   ActorTypeUser,
		ActorID:     "11111111-1111-1111-1111-111111111111",
		TargetType:  "model",
		TargetID:    "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "33333333-3333-3333-3333-333333333333",
		ProjectID:   "44444444-4444-4444-4444-444444444444",
		Payload:     map[string]any{"model_id": "claude-3-5"},
	}
	if err := sink.Write(context.Background(), ev); err != nil {
		t.Fatalf("Write: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if captPath != "/v1/logs" {
		t.Errorf("path: got %q, want /v1/logs", captPath)
	}
	if got := captHdrs.Get("Content-Type"); got != "application/x-protobuf" {
		t.Errorf("Content-Type: got %q, want application/x-protobuf", got)
	}

	// Decode the body using the OTel proto types the receiver
	// already imports — proves wire compatibility.
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(captured, &req); err != nil {
		t.Fatalf("body did not decode as OTLP/HTTP log payload: %v", err)
	}
	if len(req.GetResourceLogs()) != 1 {
		t.Fatalf("expected 1 ResourceLogs; got %d", len(req.GetResourceLogs()))
	}
	scopes := req.GetResourceLogs()[0].GetScopeLogs()
	if len(scopes) != 1 || len(scopes[0].GetLogRecords()) != 1 {
		t.Fatalf("expected 1 LogRecord; got scopes=%d records=%d",
			len(scopes), len(scopes[0].GetLogRecords()))
	}
	rec := scopes[0].GetLogRecords()[0]
	if rec.GetTimeUnixNano() != uint64(ev.OccurredAt.UnixNano()) {
		t.Errorf("TimeUnixNano: got %d, want %d", rec.GetTimeUnixNano(), uint64(ev.OccurredAt.UnixNano()))
	}
	body := rec.GetBody().GetStringValue()
	if body != "admin.model.created" {
		t.Errorf("body: got %q, want %q", body, "admin.model.created")
	}

	// Spot-check a few mandatory parsar.* attributes survived.
	attrs := map[string]string{}
	for _, kv := range rec.GetAttributes() {
		attrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	for k, want := range map[string]string{
		"parsar.audit.source":           SourceAdmin,
		"parsar.audit.event_type":       "model.created",
		"parsar.audit.actor_type":       ActorTypeUser,
		"parsar.audit.actor_id":         "11111111-1111-1111-1111-111111111111",
		"parsar.workspace_id":           "33333333-3333-3333-3333-333333333333",
		"parsar.project_id":             "44444444-4444-4444-4444-444444444444",
		"parsar.audit.payload.model_id": "claude-3-5",
	} {
		if got := attrs[k]; got != want {
			t.Errorf("attr %s: got %q, want %q", k, got, want)
		}
	}
}

// TestOTelExporterSinkSurfacesNon2xx — a misconfigured upstream
// (auth fail, bad path) must propagate as an error so MultiSink's
// logger captures the failure.
func TestOTelExporterSinkSurfacesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	sink, err := NewOTelExporterSink(OTelExporterOptions{
		Endpoint: srv.URL, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOTelExporterSink: %v", err)
	}
	err = sink.Write(context.Background(), Event{Source: SourceAdmin, EventType: "x", ActorType: ActorTypeSystem})
	if err == nil {
		t.Fatal("expected error from non-2xx upstream")
	}
}

// TestOTelExporterSinkRespectsContextCancel — a slow customer collector
// must not block the Ingester worker past its configured WriteTimeout.
func TestOTelExporterSinkRespectsContextCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-block:
		}
	}))
	defer srv.Close()
	defer close(block)

	sink, err := NewOTelExporterSink(OTelExporterOptions{
		Endpoint: srv.URL, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewOTelExporterSink: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = sink.Write(ctx, Event{Source: SourceAdmin, EventType: "x", ActorType: ActorTypeSystem})
	if err == nil {
		t.Fatal("expected error when context cancels mid-POST")
	}
}
