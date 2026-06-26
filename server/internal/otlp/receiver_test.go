package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
)

// collectingSink captures every Event so tests can assert on the
// records the receiver dispatched.
type collectingSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *collectingSink) Write(_ context.Context, ev audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return nil
}

func (c *collectingSink) snapshot() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// startReceiver wires a Receiver against a fresh in-memory sink on an
// OS-assigned port and returns the listener URL, sink, a signed
// Bearer token, the claims, and a teardown closure.
func startReceiver(t *testing.T) (url string, sink *collectingSink, token string, claims TokenClaims, cleanup func()) {
	t.Helper()

	sink = &collectingSink{}
	ing := audit.NewIngester(sink, audit.Options{
		BufferCapacity: 16,
		WriteTimeout:   time.Second,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	ing.Start(ctx)

	signer, err := NewSigner("dev-otlp-test-key", SignerOptions{})
	if err != nil {
		cancel()
		t.Fatalf("NewSigner: %v", err)
	}

	recv, err := NewReceiver(Config{
		Addr:     "127.0.0.1:0",
		Ingester: ing,
		Signer:   signer,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		cancel()
		t.Fatalf("NewReceiver: %v", err)
	}
	if err := recv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}

	claims = TokenClaims{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		AgentRunID:  "44444444-4444-4444-4444-444444444444",
		SandboxID:   "sb_test",
	}
	token, err = signer.Sign(claims, time.Hour)
	if err != nil {
		cancel()
		t.Fatalf("Sign: %v", err)
	}

	url = "http://" + recv.Addr()
	cleanup = func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = recv.Shutdown(shutdownCtx)
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = ing.Stop(stopCtx)
	}
	return url, sink, token, claims, cleanup
}

// validTraceJSON returns an OTLP/JSON trace payload carrying one span
// with one valid tool_call.started event.
func validTraceJSON(t *testing.T) []byte {
	t.Helper()
	payload := map[string]any{
		"resourceSpans": []any{map[string]any{
			"scopeSpans": []any{map[string]any{
				"spans": []any{map[string]any{
					"name": "github.create_mr",
					"attributes": []any{
						kv(AttrWorkspaceID, "11111111-1111-1111-1111-111111111111"),
						kv(AttrRequester, "33333333-3333-3333-3333-333333333333"),
						kv(AttrExecutor, "project_bot"),
						kv(AttrToolCallID, "tc_abc"),
						kv(AttrToolCallAct, "create_merge_request"),
					},
					"events": []any{map[string]any{
						"name":         "tool_call.started",
						"timeUnixNano": "1700000000000000000",
					}},
				}},
			}},
		}},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal valid payload: %v", err)
	}
	return b
}

func kv(key, value string) map[string]any {
	return map[string]any{
		"key":   key,
		"value": map[string]any{"stringValue": value},
	}
}

// post POSTs body to the OTLP traces endpoint and returns the parsed
// partial-success response plus the HTTP status.
type partialSuccessResponse struct {
	PartialSuccess struct {
		RejectedLogRecords int    `json:"rejectedLogRecords"`
		ErrorMessage       string `json:"errorMessage"`
	} `json:"partialSuccess"`
}

func post(t *testing.T, url, contentType, token string, body []byte) (int, partialSuccessResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer res.Body.Close()
	var ps partialSuccessResponse
	if err := json.NewDecoder(res.Body).Decode(&ps); err != nil && res.StatusCode == http.StatusOK {
		t.Fatalf("decode response: %v", err)
	}
	return res.StatusCode, ps
}

// waitForEvents polls until the sink has at least n events or the
// deadline fires. The audit ingester runs on its own goroutine.
func waitForEvents(t *testing.T, sink *collectingSink, n int) []audit.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		evs := sink.snapshot()
		if len(evs) >= n {
			return evs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited for %d events, only saw %d", n, len(sink.snapshot()))
	return nil
}

// TestReceiver_AcceptsValidJSONTrace confirms a happy-path OTLP/JSON
// trace round-trips through receiver → ingester → sink, and that
// token claims override OTLP attributes on the dispatched event.
func TestReceiver_AcceptsValidJSONTrace(t *testing.T) {
	base, sink, token, claims, cleanup := startReceiver(t)
	defer cleanup()

	status, ps := post(t, base+PathTraces, contentTypeJSON, token, validTraceJSON(t))
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200", status)
	}
	if ps.PartialSuccess.RejectedLogRecords != 0 {
		t.Errorf("rejected count: got %d, want 0", ps.PartialSuccess.RejectedLogRecords)
	}

	events := waitForEvents(t, sink, 1)
	if events[0].EventType != "tool_call.started" {
		t.Errorf("event_type: got %q", events[0].EventType)
	}
	if events[0].TargetID != "tc_abc" {
		t.Errorf("target_id: got %q", events[0].TargetID)
	}
	if events[0].WorkspaceID != claims.WorkspaceID {
		t.Errorf("workspace_id should reflect token claims, got %q want %q",
			events[0].WorkspaceID, claims.WorkspaceID)
	}
	if got := events[0].Payload["agent_run_id"]; got != claims.AgentRunID {
		t.Errorf("payload.agent_run_id: got %v want %q", got, claims.AgentRunID)
	}
	if got := events[0].Payload["sandbox_id"]; got != claims.SandboxID {
		t.Errorf("payload.sandbox_id: got %v want %q", got, claims.SandboxID)
	}
}

// TestReceiver_AcceptsEmptyContentType verifies the receiver falls
// through to JSON when Content-Type is missing, so `curl --data-binary
// @body.json` works without the `-H` flag.
func TestReceiver_AcceptsEmptyContentType(t *testing.T) {
	base, sink, token, _, cleanup := startReceiver(t)
	defer cleanup()

	status, ps := post(t, base+PathTraces, "", token, validTraceJSON(t))
	if status != http.StatusOK {
		t.Fatalf("status: got %d", status)
	}
	if ps.PartialSuccess.RejectedLogRecords != 0 {
		t.Errorf("rejected count: got %d", ps.PartialSuccess.RejectedLogRecords)
	}
	waitForEvents(t, sink, 1)
}

// TestReceiver_RejectsUnknownContentType ensures bogus Content-Type
// values fail with HTTP 400.
func TestReceiver_RejectsUnknownContentType(t *testing.T) {
	base, _, token, _, cleanup := startReceiver(t)
	defer cleanup()

	status, _ := post(t, base+PathTraces, "text/plain", token, []byte("nope"))
	if status != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", status)
	}
}

// TestReceiver_SchemaErrorReportedInPartialSuccess sends a payload
// missing a required attribute and verifies the OTLP partial-success
// response surfaces the rejection count back to the caller.
func TestReceiver_SchemaErrorReportedInPartialSuccess(t *testing.T) {
	base, sink, token, _, cleanup := startReceiver(t)
	defer cleanup()

	payload := map[string]any{
		"resourceSpans": []any{map[string]any{
			"scopeSpans": []any{map[string]any{
				"spans": []any{map[string]any{
					"attributes": []any{
						// workspace_id intentionally missing
						kv(AttrRequester, "33333333-3333-3333-3333-333333333333"),
						kv(AttrExecutor, "project_bot"),
						kv(AttrToolCallID, "tc_xyz"),
						kv(AttrToolCallAct, "list_repo"),
					},
					"events": []any{map[string]any{
						"name": "tool_call.started", "timeUnixNano": "1",
					}},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	status, ps := post(t, base+PathTraces, contentTypeJSON, token, body)
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (rejections are partial-success not transport failure)", status)
	}
	if ps.PartialSuccess.RejectedLogRecords != 1 {
		t.Errorf("rejected count: got %d, want 1", ps.PartialSuccess.RejectedLogRecords)
	}
	if ps.PartialSuccess.ErrorMessage == "" {
		t.Errorf("errorMessage should describe partial failure")
	}
	// Nothing should have reached the sink.
	time.Sleep(50 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("sink should be empty, got %d events: %+v", len(got), got)
	}
}

// TestReceiver_LogsPathIsStubButReplies asserts /v1/logs accepts
// well-formed payloads, counts records into the rejected total, and
// returns 200 — the stub behavior.
func TestReceiver_LogsPathIsStubButReplies(t *testing.T) {
	base, sink, token, _, cleanup := startReceiver(t)
	defer cleanup()

	payload := map[string]any{
		"resourceLogs": []any{map[string]any{
			"scopeLogs": []any{map[string]any{
				"logRecords": []any{
					map[string]any{"body": map[string]any{"stringValue": "x"}},
					map[string]any{"body": map[string]any{"stringValue": "y"}},
				},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	status, ps := post(t, base+PathLogs, contentTypeJSON, token, body)
	if status != http.StatusOK {
		t.Fatalf("status: got %d", status)
	}
	if ps.PartialSuccess.RejectedLogRecords != 2 {
		t.Errorf("rejected count: got %d, want 2 (stub drops every record)",
			ps.PartialSuccess.RejectedLogRecords)
	}
	time.Sleep(50 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("sink should be empty for log stub, got %d events", len(got))
	}
}

// TestReceiver_StartTwiceErrors guards against double-Start leaking
// the first listener.
func TestReceiver_StartTwiceErrors(t *testing.T) {
	sink := &collectingSink{}
	ing := audit.NewIngester(sink, audit.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ing.Start(ctx)
	defer func() {
		_ = ing.Stop(context.Background())
	}()

	signer, err := NewSigner("k", SignerOptions{})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	recv, err := NewReceiver(Config{Addr: "127.0.0.1:0", Ingester: ing, Signer: signer})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	if err := recv.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer recv.Shutdown(context.Background())

	if err := recv.Start(ctx); err == nil {
		t.Errorf("second Start should error")
	}
}

// TestReceiver_NilIngesterRejected verifies the constructor's
// defensive check — a nil ingester would silently lose every event.
func TestReceiver_NilIngesterRejected(t *testing.T) {
	signer, _ := NewSigner("k", SignerOptions{})
	if _, err := NewReceiver(Config{Signer: signer}); err == nil {
		t.Errorf("expected error when Ingester is nil")
	}
}

// TestReceiver_NilSignerRejected — auth is mandatory; operators must
// not be able to start the receiver in an "open" mode by accident.
func TestReceiver_NilSignerRejected(t *testing.T) {
	sink := &collectingSink{}
	ing := audit.NewIngester(sink, audit.Options{})
	if _, err := NewReceiver(Config{Ingester: ing}); err == nil {
		t.Errorf("expected error when Signer is nil")
	}
}

// TestReceiver_MissingAuthorizationRejected confirms the auth gate is
// unconditional — a well-formed OTLP body is dropped without auth.
func TestReceiver_MissingAuthorizationRejected(t *testing.T) {
	base, sink, _, _, cleanup := startReceiver(t)
	defer cleanup()
	status, _ := post(t, base+PathTraces, contentTypeJSON, "", validTraceJSON(t))
	if status != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", status)
	}
	time.Sleep(50 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("sink should be empty on unauthenticated request, got %d events", len(got))
	}
}

// TestReceiver_WrongSchemeRejected covers RFC 7235: a non-Bearer
// scheme MUST 401.
func TestReceiver_WrongSchemeRejected(t *testing.T) {
	base, _, _, _, cleanup := startReceiver(t)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPost, base+PathTraces,
		bytes.NewReader(validTraceJSON(t)))
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", res.StatusCode)
	}
}

// TestReceiver_TamperedBearerRejected proves a token whose payload or
// signature has been modified is rejected at the middleware.
func TestReceiver_TamperedBearerRejected(t *testing.T) {
	base, sink, token, _, cleanup := startReceiver(t)
	defer cleanup()

	// Flip a payload byte — still valid base64url but invalid HMAC.
	tampered := "A" + token[1:]
	status, _ := post(t, base+PathTraces, contentTypeJSON, tampered, validTraceJSON(t))
	if status != http.StatusUnauthorized {
		t.Errorf("tampered token status: got %d, want 401", status)
	}
	time.Sleep(50 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("sink should be empty, got %d events", len(got))
	}
}

// TestReceiver_TokenClaimsOverrideForgedWorkspace is the anti-forgery
// proof: a tool putting the wrong workspace_id in OTLP attributes
// MUST have it overridden by the token's claim. Without this any
// compromised producer could write audit_records against any
// workspace.
func TestReceiver_TokenClaimsOverrideForgedWorkspace(t *testing.T) {
	base, sink, token, claims, cleanup := startReceiver(t)
	defer cleanup()

	forgedAttrs := []map[string]any{
		kv(AttrWorkspaceID, "99999999-9999-9999-9999-999999999999"),
		kv(AttrRequester, "33333333-3333-3333-3333-333333333333"),
		kv(AttrExecutor, "project_bot"),
		kv(AttrToolCallID, "tc_forged"),
		kv(AttrToolCallAct, "create_merge_request"),
	}
	payload := map[string]any{
		"resourceSpans": []any{map[string]any{
			"scopeSpans": []any{map[string]any{
				"spans": []any{map[string]any{
					"attributes": forgedAttrs,
					"events": []any{map[string]any{
						"name": "tool_call.started", "timeUnixNano": "1",
					}},
				}},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	status, _ := post(t, base+PathTraces, contentTypeJSON, token, body)
	if status != http.StatusOK {
		t.Fatalf("status: got %d", status)
	}
	events := waitForEvents(t, sink, 1)
	if events[0].WorkspaceID == "99999999-9999-9999-9999-999999999999" {
		t.Fatalf("forged workspace_id was NOT overridden — anti-forgery broken")
	}
	if events[0].WorkspaceID != claims.WorkspaceID {
		t.Errorf("workspace_id: got %q, want %q (token's)", events[0].WorkspaceID, claims.WorkspaceID)
	}
}
