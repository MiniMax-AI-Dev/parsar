package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestCancelAgentRunEndpoint(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithAuditIngester(ingester))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{"reason":"user_clicked_cancel"}`, ids.UserID))
	if res.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", res.Code, res.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "cancelled" || payload["run_id"] != runID {
		t.Fatalf("unexpected cancel response: %+v", payload)
	}
	waitForRunStatus(t, db, runID, "cancelled")
	eventPayload := waitForRunEventKind(t, db, runID, "run.cancelled")
	if eventPayload["reason"] != "user_clicked_cancel" || eventPayload["source"] != "admin_cancel" {
		t.Fatalf("run.cancelled payload = %+v, want reason + source", eventPayload)
	}
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent_run.cancelled", 1)
}

func TestCancelAgentRunEndpointRejectsFinishedMissingAndNonMember(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'completed', finished_at = now() where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	finished := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, ids.UserID))
	if finished.Code != http.StatusUnprocessableEntity {
		t.Fatalf("finished cancel status = %d body=%s, want 422", finished.Code, finished.Body.String())
	}

	missing := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/00000000-0000-0000-0000-000000099999/cancel", `{}`, ids.UserID))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing cancel status = %d body=%s, want 404", missing.Code, missing.Body.String())
	}

	outsiderID := "00000000-0000-0000-0000-000000000102"
	insertConversationMessageUser(t, db, ids, outsiderID, "outsider@example.com", "")
	forbidden := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, outsiderID))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("outsider cancel status = %d body=%s, want 403", forbidden.Code, forbidden.Body.String())
	}
}

func TestRequeueAgentRunEndpointRecordsLifecycleEventAndRequiresMember(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'failed', failure_reason = 'first failure', finished_at = now() where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	outsiderID := "00000000-0000-0000-0000-000000000102"
	insertConversationMessageUser(t, db, ids, outsiderID, "outsider@example.com", "")
	forbidden := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/requeue", `{"reason":"manual_retry_from_test"}`, outsiderID))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("outsider requeue status = %d body=%s, want 403", forbidden.Code, forbidden.Body.String())
	}

	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/requeue", `{"reason":"manual_retry_from_test"}`, ids.UserID))
	if res.Code != http.StatusOK {
		t.Fatalf("requeue status = %d body=%s, want 200", res.Code, res.Body.String())
	}
	waitForRunStatus(t, db, runID, "queued")
	eventPayload := waitForRunEventKind(t, db, runID, "run.requeued")
	if eventPayload["reason"] != "manual_retry_from_test" || eventPayload["source"] != "dev_retry" {
		t.Fatalf("run.requeued payload = %+v, want reason + source", eventPayload)
	}
}

type abortSpyConnector struct {
	mu      sync.Mutex
	aborted []connector.AbortInput
	err     error
}

func (a *abortSpyConnector) Type() string { return "agent_daemon" }
func (a *abortSpyConnector) Capabilities() connector.Capabilities {
	return connector.Capabilities{Sync: true, Cancellation: true}
}
func (a *abortSpyConnector) Prompt(context.Context, connector.PromptInput) (connector.PromptOutput, error) {
	return connector.PromptOutput{}, connector.ErrNotSupported
}
func (a *abortSpyConnector) StreamPrompt(context.Context, connector.PromptInput) (<-chan connector.PromptEvent, error) {
	return nil, connector.ErrNotSupported
}
func (a *abortSpyConnector) Cancel(context.Context, string) error { return nil }
func (a *abortSpyConnector) Abort(_ context.Context, in connector.AbortInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.aborted = append(a.aborted, in)
	return a.err
}
func (a *abortSpyConnector) SubmitPermission(context.Context, connector.PermissionDecision) error {
	return connector.ErrNotSupported
}
func (a *abortSpyConnector) SubmitPromptForUserChoice(context.Context, connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}
func (a *abortSpyConnector) Close(context.Context, string) error { return nil }

func TestCancelAgentRunEndpointAbortsOpenCodeConnector(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	spy := &abortSpyConnector{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithConnectorRegistry(testConnectorRegistry(t, spy)))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running', connector_type = 'agent_daemon' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, ids.UserID))
	if res.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", res.Code, res.Body.String())
	}
	if len(spy.aborted) != 1 || spy.aborted[0].ConversationID != ids.ConversationID || spy.aborted[0].RunID != runID {
		t.Fatalf("abort calls = %+v, want one call for run", spy.aborted)
	}
}

func TestCancelAgentRunConcurrentRequestsAreIdempotent(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	spy := &abortSpyConnector{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithConnectorRegistry(testConnectorRegistry(t, spy)), WithAuditIngester(ingester))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running', connector_type = 'agent_daemon' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	type cancelRaceResponse struct {
		code          int
		status        string
		errorMessage  string
		currentStatus string
		body          string
	}
	responses := make(chan cancelRaceResponse, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, ids.UserID))
			var payload map[string]string
			if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
				t.Error(err)
				return
			}
			responses <- cancelRaceResponse{
				code:          res.Code,
				status:        payload["status"],
				errorMessage:  payload["error"],
				currentStatus: payload["current_status"],
				body:          res.Body.String(),
			}
		}()
	}
	wg.Wait()
	close(responses)
	cancelled, benignLosers := 0, 0
	for resp := range responses {
		switch {
		case resp.code == http.StatusOK && resp.status == "cancelled":
			cancelled++
		case resp.code == http.StatusOK && resp.status == "already_cancelling":
			benignLosers++
		case resp.code == http.StatusUnprocessableEntity && resp.errorMessage == "run already finished" && resp.currentStatus == "cancelled":
			benignLosers++
		default:
			t.Fatalf("unexpected concurrent cancel response: code=%d body=%s", resp.code, resp.body)
		}
	}
	if cancelled != 1 || benignLosers != 9 {
		t.Fatalf("concurrent cancel responses: cancelled=%d benign_losers=%d, want 1/9", cancelled, benignLosers)
	}
	spy.mu.Lock()
	abortCalls := len(spy.aborted)
	spy.mu.Unlock()
	if abortCalls != 1 {
		t.Fatalf("abort calls = %d, want 1", abortCalls)
	}
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent_run.cancelled", 1)
}

func TestCancelAgentRunAuditRecordsAbortFailure(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	spy := &abortSpyConnector{err: errors.New("abort failed")}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithConnectorRegistry(testConnectorRegistry(t, spy)), WithAuditIngester(ingester))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running', connector_type = 'agent_daemon' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, ids.UserID))
	if res.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", res.Code, res.Body.String())
	}
	flushDevRouteAudit(t, ingester)
	var abortOK bool
	if err := db.QueryRow(ctx, `select (payload->>'sandbox_abort_ok')::boolean from audit_records where event_type = 'agent_run.cancelled' and target_id = $1::uuid`, runID).Scan(&abortOK); err != nil {
		t.Fatal(err)
	}
	if abortOK {
		t.Fatal("expected sandbox_abort_ok=false when connector abort fails")
	}
}

// raceCancelConnector simulates the real opencode dispatcher race:
// StreamPrompt opens a channel that holds open until ctx cancels;
// when Abort is called, it cancels the per-stream ctx, the goroutine
// then emits one EventError("context canceled") (mirroring how a
// real opencode subprocess reports its parent ctx termination) and
// closes. This is the path the dispatcher in run_stream.go:244-256
// hits in production — and the path that, prior to the FailAgentRun
// SQL guard, would silently overwrite status='cancelled' with
// 'failed'.
type raceCancelConnector struct {
	mu            sync.Mutex
	streamCancel  context.CancelFunc
	streamStarted chan struct{}
}

func (c *raceCancelConnector) Type() string { return "agent_daemon" }
func (c *raceCancelConnector) Capabilities() connector.Capabilities {
	return connector.Capabilities{Sync: true, Streaming: true, Cancellation: true}
}
func (c *raceCancelConnector) Prompt(context.Context, connector.PromptInput) (connector.PromptOutput, error) {
	return connector.PromptOutput{}, connector.ErrNotSupported
}
func (c *raceCancelConnector) Cancel(context.Context, string) error { return nil }
func (c *raceCancelConnector) SubmitPermission(context.Context, connector.PermissionDecision) error {
	return connector.ErrNotSupported
}
func (c *raceCancelConnector) SubmitPromptForUserChoice(context.Context, connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}
func (c *raceCancelConnector) Close(context.Context, string) error { return nil }
func (c *raceCancelConnector) StreamPrompt(parent context.Context, _ connector.PromptInput) (<-chan connector.PromptEvent, error) {
	streamCtx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	c.streamCancel = cancel
	c.mu.Unlock()
	out := make(chan connector.PromptEvent, 4)
	go func() {
		defer close(out)
		// Signal that the dispatcher has entered the for-range loop
		// so the test can move on to /cancel without racing the
		// MarkAgentRunRunning vs goroutine spawn ordering.
		select {
		case out <- connector.PromptEvent{Type: connector.EventDelta, Delta: "starting"}:
		case <-streamCtx.Done():
			return
		}
		<-streamCtx.Done()
		// After cancel, emit the same EventError shape a real
		// opencode CLI would (its child process catches the parent
		// ctx termination and reports "context canceled" via the
		// stream channel before exiting).
		out <- connector.PromptEvent{Type: connector.EventError, Error: "context canceled"}
	}()
	if c.streamStarted != nil {
		close(c.streamStarted)
		c.streamStarted = nil
	}
	return out, nil
}
func (c *raceCancelConnector) Abort(_ context.Context, _ connector.AbortInput) error {
	c.mu.Lock()
	cancel := c.streamCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// TestCancelAgentRunPreservesCancelledStatusOverDispatcherFailRace pins
// the round-2 reviewer finding: when /cancel sets status='cancelled'
// and Abort cancels the stream ctx, the dispatcher goroutine
// receives EventError("context canceled") and calls FailAgentRun.
// That call MUST be a no-op — the SQL guard
// `status not in ('completed', 'cancelled')` keeps status='cancelled'
// intact, and audit must NOT emit a stray conversation_stream.failed
// event (which would break "failed audit count == failed run count"
// admin invariants).
//
// Earlier versions of the cancel-button feature passed unit tests
// because every test used createStreamTestRun (no /start) plus a
// hand-set status='running' and abortSpyConnector — never spawning
// the dispatcher goroutine that produces this race in production.
// This test takes the production path: real /start → goroutine →
// real Abort → real EventError → real FailAgentRun.
func TestCancelAgentRunPreservesCancelledStatusOverDispatcherFailRace(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	rc := &raceCancelConnector{streamStarted: make(chan struct{})}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithConnectorRegistry(testConnectorRegistry(t, rc)), WithAuditIngester(ingester))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set connector_type = 'agent_daemon' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}

	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	startRes := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, `{}`, ids.UserID))
	if startRes.Code != http.StatusAccepted {
		t.Fatalf("start status = %d body=%s, want 202", startRes.Code, startRes.Body.String())
	}

	// Wait for the dispatcher to enter the for-range loop — this is
	// what the production path looks like at the moment the user
	// clicks Cancel.
	select {
	case <-rc.streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher never entered StreamPrompt for-range")
	}
	waitForRunStatus(t, db, runID, "running")

	// Real cancel: handler sets status='cancelled', then calls
	// Abort which cancels stream ctx, which triggers EventError,
	// which fires the dispatcher's FailAgentRun — the very race
	// that previously overwrote 'cancelled' with 'failed'.
	cancelRes := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{"reason":"user_clicked_cancel"}`, ids.UserID))
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s, want 200", cancelRes.Code, cancelRes.Body.String())
	}

	// Give the dispatcher time to receive EventError and run
	// FailAgentRun. 200ms is generous: the goroutine emits
	// EventError immediately on streamCancel, and FailAgentRun is
	// a single SQL update.
	time.Sleep(200 * time.Millisecond)

	var status, failureReason string
	var metaBytes []byte
	if err := db.QueryRow(ctx, `select status, failure_reason, metadata from agent_runs where id = $1`, runID).Scan(&status, &failureReason, &metaBytes); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("status = %s, want 'cancelled' (FailAgentRun SQL guard regressed)", status)
	}
	if failureReason != "user_clicked_cancel" {
		t.Fatalf("failure_reason = %q, want 'user_clicked_cancel' (dispatcher overwrote cancel reason)", failureReason)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("metadata not valid json: %v", err)
	}
	if cr, ok := meta["cancel_reason"].(string); !ok || cr != "user_clicked_cancel" {
		t.Fatalf("metadata cancel_reason = %v, want 'user_clicked_cancel'", meta["cancel_reason"])
	}

	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent_run.cancelled", 1)
	// The critical invariant: NO conversation_stream.failed audit
	// even though the dispatcher's FailAgentRun call ran. Without
	// the guard, the audit would fire and admins would see one
	// "failed" event without any matching status='failed' row.
	assertAuditEventCount(t, db, "conversation_stream.failed", 0)

	// Idempotency check: a second cancel after the run is already
	// cancelled returns 422 already-finished, not 500. UI safety
	// net for users double-clicking cancel.
	secondCancel := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+runID+"/cancel", `{}`, ids.UserID))
	if secondCancel.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second cancel status = %d body=%s, want 422 already-finished", secondCancel.Code, secondCancel.Body.String())
	}
}
