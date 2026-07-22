package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConversationRunStreamStartAndLateReplayPersistsFinal(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	conn := &fakeStreamConnector{caps: connector.Capabilities{Sync: true, Streaming: true}, events: []connector.PromptEvent{
		{Type: connector.EventDelta, Sequence: 1, Delta: "part-1"},
		{Type: connector.EventDelta, Sequence: 2, Delta: "part-2"},
		{Type: connector.EventDelta, Sequence: 3, Delta: "part-3"},
		{Type: connector.EventDelta, Sequence: 4, Delta: "part-4"},
		{Type: connector.EventDelta, Sequence: 5, Delta: "part-5"},
		{Type: connector.EventDone, Sequence: 6, Final: &connector.PromptOutput{Content: "part-1part-2part-3part-4part-5"}},
	}}
	broker := runstream.NewBroker(runstream.DefaultBufferSize)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker), WithConnectorRegistry(testConnectorRegistry(t, conn)))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	start := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID))
	if start.Code != http.StatusAccepted || !strings.Contains(start.Body.String(), `"status":"running"`) {
		t.Fatalf("start status/body = %d %s, want 202 running", start.Code, start.Body.String())
	}

	waitForRunStatus(t, db, runID, "completed")
	streamPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/stream"
	stream := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodGet, streamPath, "", ids.UserID))
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status = %d body=%s, want 200", stream.Code, stream.Body.String())
	}
	if got := stream.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("stream content-type = %q, want text/event-stream", got)
	}
	events := parseSSEFrames(t, stream.Body)
	if len(events) != 6 {
		t.Fatalf("late replay got %d events, want 6: %+v", len(events), events)
	}
	for i := 0; i < 5; i++ {
		if events[i]["type"] != "delta" || events[i]["delta"] == "" {
			t.Fatalf("event %d = %+v, want delta with text", i, events[i])
		}
	}
	if events[5]["type"] != "done" {
		t.Fatalf("last event = %+v, want done", events[5])
	}
	final, ok := events[5]["final"].(map[string]any)
	if !ok || final["content"] == "" {
		t.Fatalf("done.final = %+v, want final content", events[5]["final"])
	}

	var status string
	var finishedAt *time.Time
	var assistantMessages int
	if err := db.QueryRow(ctx, `select status, finished_at from agent_runs where id = $1`, runID).Scan(&status, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || finishedAt == nil {
		t.Fatalf("run status/finished_at = %s/%v, want completed/non-nil", status, finishedAt)
	}
	if err := db.QueryRow(ctx, `select count(*) from messages where conversation_id = $1 and sender_type = 'agent' and metadata->>'run_id' = $2`, ids.ConversationID, runID).Scan(&assistantMessages); err != nil {
		t.Fatal(err)
	}
	if assistantMessages != 1 {
		t.Fatalf("assistant message count = %d, want 1", assistantMessages)
	}
	waitForRunEventKind(t, db, runID, "run.started")
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent_daemon.completed", 1)
}

func TestConversationRunStreamPromptErrorPersistsFailureEvent(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigureDevConversationExternalRef(ctx, store.ConfigureDevConversationExternalRefInput{ConversationID: ids.ConversationID, Gateway: "feishu", ExternalChatID: "oc_stream_failure"}); err != nil {
		t.Fatal(err)
	}
	conn := &fakeStreamConnector{caps: connector.Capabilities{Sync: true, Streaming: true}, errReturn: errors.New("daemon not registered")}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(runstream.NewBroker(runstream.DefaultBufferSize)), WithConnectorRegistry(testConnectorRegistry(t, conn)))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	start := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID))
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status/body = %d %s, want 202", start.Code, start.Body.String())
	}
	waitForRunStatus(t, db, runID, "failed")
	failurePayload := waitForRunEventKind(t, db, runID, "run.failed")
	if failurePayload["error"] != "daemon not registered" || failurePayload["source"] != "agent_daemon" {
		t.Fatalf("run.failed payload = %+v, want source + error", failurePayload)
	}
	// Driver-only refactor: the failure is now carried by the
	// `run.failed` event payload (consumed by the inflight driver
	// when it patches the terminal ErrorCard); we no longer emit a
	// run_failure messages-table row, so there's no P1 outbound
	// queue to drain.
	if visible, ok := failurePayload["user_visible_message"].(string); !ok || visible == "" {
		t.Fatalf("run.failed payload missing user_visible_message: %+v", failurePayload)
	}
	waitForRunEventKind(t, db, runID, "run.started")
}

// TestConversationRunStreamInBandFailureEmitsSingleRunFailed pins the
// dedupe contract that the sandbox-cache health-check fix relies on. In
// production we saw two run.failed entries for the same run — one
// "empty final output" from the EventDone-with-empty-final path and one
// "runtime deleted by admin" from the dispatcher's own emit. Operators
// then had to guess which was the real cause.
//
// After the fix, the in-band failure path (connector emitted EventError
// + an empty EventDone) flips the run row but does NOT emit its own
// run.failed event — the one written by eventPersistencePayload during
// the Done frame is authoritative. This test fails if dedup regresses.
func TestConversationRunStreamInBandFailureEmitsSingleRunFailed(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	// Mimic the sandbox-eviction failure shape: the connector opens a
	// stream, surfaces an EventError, and closes it with an empty Done.
	// Pre-fix this generated two run.failed events.
	conn := &fakeStreamConnector{
		caps: connector.Capabilities{Sync: true, Streaming: true},
		events: []connector.PromptEvent{
			{Type: connector.EventError, Sequence: 1, Error: "runtime retired"},
			{Type: connector.EventDone, Sequence: 2, Final: &connector.PromptOutput{
				Content: "",
				Metadata: map[string]any{
					"error":  "runtime retired",
					"source": "agent_daemon",
				},
			}},
		},
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(runstream.NewBroker(runstream.DefaultBufferSize)), WithConnectorRegistry(testConnectorRegistry(t, conn)))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	start := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID))
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status = %d body=%s, want 202", start.Code, start.Body.String())
	}
	waitForRunStatus(t, db, runID, "failed")
	// At least one run.failed must exist (sanity).
	_ = waitForRunEventKind(t, db, runID, "run.failed")

	// Count run.failed events for this run. Must be exactly 1.
	var count int
	if err := db.QueryRow(ctx, `select count(*) from agent_run_events where run_id = $1 and kind = 'run.failed'`, runID).Scan(&count); err != nil {
		t.Fatalf("query run.failed count: %v", err)
	}
	if count != 1 {
		// Dump the rows so the failure tells us what the dispatcher emitted.
		rows, _ := db.Query(ctx, `select kind, payload from agent_run_events where run_id = $1 order by created_at asc`, runID)
		var dump strings.Builder
		for rows != nil && rows.Next() {
			var kind string
			var payload []byte
			_ = rows.Scan(&kind, &payload)
			dump.WriteString(kind)
			dump.WriteString(": ")
			dump.Write(payload)
			dump.WriteString("\n")
		}
		t.Fatalf("expected exactly 1 run.failed event, got %d. events:\n%s", count, dump.String())
	}
}

func TestConversationRunStartPersistsEventsSynchronouslyAfterRequestContextCancel(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	delayedStore := &delayedEventStore{Store: s, started: make(chan struct{}), release: make(chan struct{})}
	conn := &fakeStreamConnector{caps: connector.Capabilities{Sync: true, Streaming: true}, events: []connector.PromptEvent{
		{Type: connector.EventDelta, Sequence: 1, Delta: "hello"},
		{Type: connector.EventDone, Sequence: 2, Final: &connector.PromptOutput{Content: "hello"}},
	}}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, delayedStore,
		WithRunStreamBroker(runstream.NewBroker(runstream.DefaultBufferSize)),
		WithConnectorRegistry(testConnectorRegistry(t, conn)),
	)

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	startCtx, cancel := context.WithCancel(ctx)
	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	startReq := newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID).WithContext(startCtx)
	type startResult struct {
		code int
		body string
	}
	startDone := make(chan startResult, 1)
	go func() {
		response := serveConversationMessageRequest(r, startReq)
		startDone <- startResult{code: response.Code, body: response.Body.String()}
	}()
	waitForDelayedEventWrite(t, delayedStore.started)
	select {
	case <-startDone:
		t.Fatal("start returned before its lifecycle event was durably recorded")
	default:
	}
	cancel()
	close(delayedStore.release)
	start := <-startDone
	if start.code != http.StatusAccepted {
		t.Fatalf("start status/body = %d %s, want 202", start.code, start.body)
	}

	events := waitForPersistedRunEvents(t, db, runID, 1)
	if events < 1 {
		t.Fatalf("expected at least one persisted event after /start returned, got %d", events)
	}
	if conn.gotInput.ConversationInitiatorID != ids.UserID {
		t.Fatalf("ConversationInitiatorID = %q, want %q", conn.gotInput.ConversationInitiatorID, ids.UserID)
	}
	waitForRunStatus(t, db, runID, "completed")

	eventsPath := "/api/v1/workspaces/" + ids.WorkspaceID + "/agent-runs/" + runID + "/events?after_sequence=0"
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodGet, eventsPath, "", ids.UserID))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"event_kind"`) {
		t.Fatalf("events status/body = %d %s, want persisted events", res.Code, res.Body.String())
	}
}

type delayedEventStore struct {
	*store.Store
	started chan struct{}
	release chan struct{}
}

func (s *delayedEventStore) RecordAgentRunEvent(ctx context.Context, input store.RecordAgentRunEventInput) error {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-s.release
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.RecordAgentRunEvent(ctx, input)
}

func TestConversationRunStreamStartRejectsNonQueuedMissingAndNonMember(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	memberID := "00000000-0000-0000-0000-000000000101"
	outsiderID := "00000000-0000-0000-0000-000000000102"
	insertConversationMessageUser(t, db, ids, memberID, "member@example.com", "member")
	insertConversationMessageUser(t, db, ids, outsiderID, "outsider@example.com", "")

	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(runstream.NewBroker(runstream.DefaultBufferSize)))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	startPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/start"
	// Idempotent /start: server-side auto-start (StreamingDispatcher)
	// races with the frontend's tolerant /start call. When the run is
	// already running, /start must return 200 + status:"running"
	// instead of 422 — otherwise the composer would surface a benign
	// race as an error toast. completed/failed still get 422 (you
	// can't restart a terminal run).
	alreadyRunning := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID))
	if alreadyRunning.Code != http.StatusOK || !strings.Contains(alreadyRunning.Body.String(), `"status":"running"`) {
		t.Fatalf("running start status/body = %d %s, want 200 status:running (idempotent no-op)",
			alreadyRunning.Code, alreadyRunning.Body.String())
	}

	if _, err := db.Exec(ctx, `update agent_runs set status = 'failed', finished_at = now() where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	terminal := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", ids.UserID))
	if terminal.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failed start status = %d body=%s, want 422", terminal.Code, terminal.Body.String())
	}

	missingPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/00000000-0000-0000-0000-000000099999/start"
	missing := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, missingPath, "", ids.UserID))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing start status = %d body=%s, want 404", missing.Code, missing.Body.String())
	}

	forbidden := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, startPath, "", outsiderID))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("outsider start status = %d body=%s, want 403", forbidden.Code, forbidden.Body.String())
	}

	streamForbidden := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodGet, "/api/v1/conversations/"+ids.ConversationID+"/runs/"+runID+"/stream", "", outsiderID))
	if streamForbidden.Code != http.StatusForbidden {
		t.Fatalf("outsider stream status = %d body=%s, want 403", streamForbidden.Code, streamForbidden.Body.String())
	}
}

func createStreamTestRun(t *testing.T, r http.Handler, ids store.DevFixtureIDs, userID string) string {
	t.Helper()
	path := "/api/v1/conversations/" + ids.ConversationID + "/messages"
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{"content":"please @Product Agent reply in streaming mode"}`, userID))
	if res.Code != http.StatusCreated {
		t.Fatalf("create message status = %d body=%s, want 201", res.Code, res.Body.String())
	}
	var payload struct {
		AgentRunID string `json:"agent_run_id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AgentRunID == "" {
		t.Fatalf("response missing agent_run_id: %s", res.Body.String())
	}
	return payload.AgentRunID
}

func waitForRunStatus(t *testing.T, db *pgxpool.Pool, runID string, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var got string
		if err := db.QueryRow(context.Background(), `select status from agent_runs where id = $1`, runID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run status timed out: got %s, want %s", got, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForRunEventKind(t *testing.T, db *pgxpool.Pool, runID string, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var payloadBytes []byte
		err := db.QueryRow(context.Background(), `select payload from agent_run_events where agent_run_id = $1 and event_kind = $2 order by sequence desc limit 1`, runID, kind).Scan(&payloadBytes)
		if err == nil {
			var payload map[string]any
			if err := json.Unmarshal(payloadBytes, &payload); err != nil {
				t.Fatalf("%s payload is not valid json: %v", kind, err)
			}
			return payload
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query %s event: %v", kind, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s event", kind)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForPersistedRunEvents(t *testing.T, db *pgxpool.Pool, runID string, minimum int) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var got int
		if err := db.QueryRow(context.Background(), `select count(*) from agent_run_events where agent_run_id = $1`, runID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= minimum {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForDelayedEventWrite(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event side-write goroutine to start")
	}
}

// TestConversationRunStreamHangsAreBoundedByFirstEventTimeout pins
// the reviewer round-1 hang-protection contract: when /stream
// subscribes to a runID for which no dispatcher ever publishes
// (e.g. /start failed silently, runID is stale, server crashed
// between /start and /stream), the handler must NOT block until
// client disconnect. Instead it must emit one synthesized error
// frame and close within streamFirstEventTimeout.
//
// We shrink streamFirstEventTimeout to 150ms so the test stays
// snappy. Original value (30s) is restored via t.Cleanup so other
// tests aren't affected.
func TestConversationRunStreamHangsAreBoundedByFirstEventTimeout(t *testing.T) {
	original := streamFirstEventTimeout
	streamFirstEventTimeout = 150 * time.Millisecond
	t.Cleanup(func() { streamFirstEventTimeout = original })

	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	broker := runstream.NewBroker(runstream.DefaultBufferSize)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker))

	// Create a run via the messages endpoint but DO NOT call /start.
	// The run stays queued forever from the broker's perspective —
	// exactly the failure mode reviewer round-1 called out.
	runID := createStreamTestRun(t, r, ids, ids.UserID)

	streamPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/stream"
	start := time.Now()
	stream := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodGet, streamPath, "", ids.UserID))
	elapsed := time.Since(start)

	if stream.Code != http.StatusOK {
		t.Fatalf("stream status = %d body=%s, want 200 (hang protection still flushes headers)", stream.Code, stream.Body.String())
	}
	// Must close within roughly 2× the deadline; allow generous
	// slack for CI scheduling jitter, but anything ≥ 5s means the
	// hang protection didn't fire.
	if elapsed > 5*time.Second {
		t.Fatalf("stream handler took %s to return; expected ≤ ~2×%s", elapsed, streamFirstEventTimeout)
	}
	events := parseSSEFrames(t, stream.Body)
	if len(events) != 1 {
		t.Fatalf("hang-bounded stream got %d events, want 1 synthesized error: %+v", len(events), events)
	}
	if events[0]["type"] != "error" {
		t.Fatalf("synthesized event = %+v, want type=error", events[0])
	}
	reason, _ := events[0]["error"].(string)
	if reason == "" {
		t.Fatalf("synthesized event missing error reason: %+v", events[0])
	}
	// Verify the run is still queued — hang protection should NOT
	// mutate run state, just bound the handler lifetime.
	var status string
	if err := db.QueryRow(ctx, `select status from agent_runs where id = $1`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("run status after hang protection = %s, want queued (handler must not mutate run)", status)
	}
}

// TestConversationRunStreamSurfacesFailedRunReason covers the second
// hang-protection branch: when the run has already been moved to
// 'failed' by some other path (e.g. a sandbox spawn that crashed
// before the broker was attached) but no event was ever published,
// /stream should refresh status from the store and surface a
// failed-specific reason instead of the generic timeout.
func TestConversationRunStreamSurfacesFailedRunReason(t *testing.T) {
	original := streamFirstEventTimeout
	streamFirstEventTimeout = 150 * time.Millisecond
	t.Cleanup(func() { streamFirstEventTimeout = original })

	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	broker := runstream.NewBroker(runstream.DefaultBufferSize)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker))

	runID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `update agent_runs set status = 'failed', metadata = metadata || jsonb_build_object('error', 'external dispatch crashed') where id = $1`, runID); err != nil {
		t.Fatal(err)
	}

	streamPath := "/api/v1/conversations/" + ids.ConversationID + "/runs/" + runID + "/stream"
	stream := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodGet, streamPath, "", ids.UserID))
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status = %d body=%s, want 200", stream.Code, stream.Body.String())
	}
	events := parseSSEFrames(t, stream.Body)
	if len(events) != 1 || events[0]["type"] != "error" {
		t.Fatalf("hang-bounded stream events = %+v, want one error frame", events)
	}
	reason, _ := events[0]["error"].(string)
	if !strings.Contains(reason, "failed") {
		t.Fatalf("synthesized reason = %q, want it to mention the failed run state", reason)
	}
}

// TestStartConversationRunQueuesWhenInflightSiblingExists verifies that
// when a new run starts for the same (conversation, agent) and
// a running predecessor exists, the new run stays queued (no
// supersede) so the in-flight run can complete its work undisturbed.
// The old run's terminator will pick up the queued run via
// dispatchNextQueuedRunAfter.
func TestStartConversationRunQueuesWhenInflightSiblingExists(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	abortConn := &abortCapturingConnector{fakeStreamConnector: fakeStreamConnector{
		caps:   connector.Capabilities{Sync: true, Streaming: true},
		events: []connector.PromptEvent{{Type: connector.EventDone, Sequence: 1, Final: &connector.PromptOutput{Content: "ok"}}},
	}}
	broker := runstream.NewBroker(runstream.DefaultBufferSize)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker), WithConnectorRegistry(testConnectorRegistry(t, abortConn)))

	// Create first run and manually set it to "running" to simulate
	// an in-flight prompt.
	oldRunID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `UPDATE agent_runs SET status = 'running', started_at = now() WHERE id = $1`, oldRunID); err != nil {
		t.Fatal(err)
	}

	// Create a second run — this simulates the user sending a new message
	// while the first one is still being processed.
	newRunID := createStreamTestRun(t, r, ids, ids.UserID)

	// Start the new run via StartConversationRun.
	deps := StreamingDispatchDeps{
		Broker:            broker,
		ConnectorRegistry: testConnectorRegistry(t, abortConn),
		DispatchCtx:       ctx,
	}
	status, err := StartConversationRun(ctx, s, deps, newRunID, ids.ConversationID)
	if err != nil {
		t.Fatalf("StartConversationRun failed: %v", err)
	}
	if status != "queued" {
		t.Fatalf("new run status = %q, want queued (blocked by in-flight sibling)", status)
	}

	// Assert: old run is still running, NOT cancelled.
	var oldStatus string
	if err := db.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, oldRunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "running" {
		t.Fatalf("old run status = %q, want running (must not be superseded)", oldStatus)
	}

	// Assert: new run is queued in DB.
	var newStatus string
	if err := db.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, newRunID).Scan(&newStatus); err != nil {
		t.Fatal(err)
	}
	if newStatus != "queued" {
		t.Fatalf("new run DB status = %q, want queued", newStatus)
	}

	// Assert: Abort was NOT called (no supersede happened).
	if len(abortConn.abortCalls) != 0 {
		t.Fatalf("Abort should not be called when queueing, got %d calls", len(abortConn.abortCalls))
	}
}

// TestStartConversationRunNoQueueCrossConversation verifies that
// runs in different conversations do NOT block each other — the
// serial queue is per (conversation, agent), not global.
func TestStartConversationRunNoQueueCrossConversation(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	abortConn := &abortCapturingConnector{fakeStreamConnector: fakeStreamConnector{
		caps:   connector.Capabilities{Sync: true, Streaming: true},
		events: []connector.PromptEvent{{Type: connector.EventDone, Sequence: 1, Final: &connector.PromptOutput{Content: "ok"}}},
	}}
	broker := runstream.NewBroker(runstream.DefaultBufferSize)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker), WithConnectorRegistry(testConnectorRegistry(t, abortConn)))

	// Create a run in the default conversation and set it to running.
	oldRunID := createStreamTestRun(t, r, ids, ids.UserID)
	if _, err := db.Exec(ctx, `UPDATE agent_runs SET status = 'running', started_at = now() WHERE id = $1`, oldRunID); err != nil {
		t.Fatal(err)
	}

	// Create a second conversation + run via raw SQL.
	conv2ID := "00000000-0000-0000-0000-000000066666"
	newRunID := "00000000-0000-0000-0000-000000077777"
	if _, err := db.Exec(ctx, `INSERT INTO conversations(id, workspace_id, title, created_at, updated_at) VALUES ($1::uuid, $2::uuid, 'conv-2', now(), now())`, conv2ID, ids.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO agent_runs(id, workspace_id, conversation_id, trigger_message_id, trigger_channel, requested_by_type, requested_by_id, agent_id, connector_type, status, metadata, created_at, updated_at)
		SELECT $1::uuid, workspace_id, $2::uuid, trigger_message_id, 'web', requested_by_type, requested_by_id, agent_id, connector_type, 'queued', '{}', now(), now()
		FROM agent_runs WHERE id = $3`, newRunID, conv2ID, oldRunID); err != nil {
		t.Fatal(err)
	}

	deps := StreamingDispatchDeps{
		Broker:            broker,
		ConnectorRegistry: testConnectorRegistry(t, abortConn),
		DispatchCtx:       ctx,
	}
	status, err := StartConversationRun(ctx, s, deps, newRunID, conv2ID)
	if err != nil {
		t.Fatalf("StartConversationRun failed: %v", err)
	}
	if status != "running" {
		t.Fatalf("new run status = %q, want running (different conversation should not be queued)", status)
	}

	// Old run in a different conversation must still be running.
	var oldStatus string
	if err := db.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, oldRunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "running" {
		t.Fatalf("cross-conversation run status = %q, want running (must not be superseded)", oldStatus)
	}
	if len(abortConn.abortCalls) != 0 {
		t.Fatalf("Abort should not be called for cross-conversation run, got %d calls", len(abortConn.abortCalls))
	}
}

// abortCapturingConnector wraps fakeStreamConnector and records Abort calls.
type abortCapturingConnector struct {
	fakeStreamConnector
	abortCalls []connector.AbortInput
}

func (c *abortCapturingConnector) Abort(_ context.Context, in connector.AbortInput) error {
	c.abortCalls = append(c.abortCalls, in)
	return nil
}

// TestEventPersistencePayload_Thinking pins the mapping
// EventThinking → ("message.thinking", {thinking, sequence}). Without
// this row, the IM gateway's claim filter never wakes on
// thinking-only runs and the DoneCard cannot pull the reasoning
// trace back out — both gating behaviours rely on the event_kind
// string being exactly "message.thinking".
func TestEventPersistencePayload_Thinking(t *testing.T) {
	t.Parallel()
	ev := connector.PromptEvent{
		Type:     connector.EventThinking,
		Thinking: "The user said hi. Reply briefly.",
		Sequence: 42,
	}
	kind, payload, ok := eventPersistencePayload(ev)
	if !ok {
		t.Fatal("eventPersistencePayload returned ok=false for EventThinking")
	}
	if kind != "message.thinking" {
		t.Errorf("event_kind = %q, want \"message.thinking\"", kind)
	}
	if got, _ := payload["thinking"].(string); got != "The user said hi. Reply briefly." {
		t.Errorf("payload[\"thinking\"] = %q, want the raw text", got)
	}
	if got, _ := payload["sequence"].(uint64); got != 42 {
		t.Errorf("payload[\"sequence\"] = %v, want 42", payload["sequence"])
	}
}

type failingRunEventRecorder struct{ err error }

func (r failingRunEventRecorder) RecordAgentRunEvent(context.Context, store.RecordAgentRunEventInput) error {
	return r.err
}

func TestRecordPromptEventReturnsCanonicalInteractionWriteFailure(t *testing.T) {
	want := errors.New("database unavailable")
	err := recordPromptEvent(context.Background(), failingRunEventRecorder{err: want}, "run-1", connector.PromptEvent{
		Type:       connector.EventPermissionRequest,
		Permission: &connector.PermissionRequest{ID: "perm-1", DeviceID: "device-1"},
	})
	if !errors.Is(err, want) {
		t.Fatalf("recordPromptEvent error = %v, want %v", err, want)
	}
}

// TestEventPersistencePayload_RunFailedCarriesUserVisibleMessage pins
// the contract: an EventDone without final content emits a payload that
// includes a translated user_visible_message. The Feishu inflight driver
// consumes this to render the terminal ErrorCard; without it the card
// degrades to a generic "Agent run failed".
func TestEventPersistencePayload_RunFailedCarriesUserVisibleMessage(t *testing.T) {
	t.Parallel()
	ev := connector.PromptEvent{
		Type:     connector.EventDone,
		Sequence: 7,
		Final: &connector.PromptOutput{
			Content: "",
			Metadata: map[string]any{
				"error":  "capability_credential_missing",
				"source": "agent_runtime",
			},
		},
	}
	kind, payload, ok := eventPersistencePayload(ev)
	if !ok {
		t.Fatal("eventPersistencePayload returned ok=false for EventDone (failed)")
	}
	if kind != "run.failed" {
		t.Errorf("event_kind = %q, want \"run.failed\"", kind)
	}
	if got, _ := payload["error"].(string); got != "capability_credential_missing" {
		t.Errorf("payload[\"error\"] = %q, want raw reason preserved", got)
	}
	if got, _ := payload["user_visible_message"].(string); got == "" {
		t.Error("payload missing user_visible_message — driver ErrorCard would fall back to generic copy")
	} else if got == "capability_credential_missing" {
		t.Errorf("payload[\"user_visible_message\"] = %q, want it translated by mapUserFacingReason (e.g. mentions credential)", got)
	}
	if got, _ := payload["source"].(string); got != "agent_runtime" {
		t.Errorf("payload[\"source\"] = %q, want agent_runtime preserved", got)
	}
}

// TestEventPersistencePayload_RunFailedTranslatesEmptyError covers
// the defensive fallback: EventDone with no Metadata at all still
// emits a payload with the canonical "unknown failure" copy in
// user_visible_message rather than leaving it absent.
func TestEventPersistencePayload_RunFailedTranslatesEmptyError(t *testing.T) {
	t.Parallel()
	ev := connector.PromptEvent{Type: connector.EventDone, Sequence: 9}
	kind, payload, ok := eventPersistencePayload(ev)
	if !ok {
		t.Fatal("eventPersistencePayload returned ok=false for EventDone (no final)")
	}
	if kind != "run.failed" {
		t.Errorf("event_kind = %q, want \"run.failed\"", kind)
	}
	if got, _ := payload["user_visible_message"].(string); got == "" {
		t.Error("payload missing user_visible_message; want a non-empty fallback translation")
	}
}
