package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type retryTestDispatcher func(context.Context, store.StreamingDispatchInput)

func (f retryTestDispatcher) Start(ctx context.Context, in store.StreamingDispatchInput) { f(ctx, in) }

func createRetryTestRun(t *testing.T, s *store.Store, ids store.DevFixtureIDs) string {
	t.Helper()
	result, err := s.SendUserMessageToConversation(t.Context(), store.SendUserMessageToConversationInput{
		ConversationID: ids.ConversationID, UserID: ids.UserID, Content: "Retry this synthetic request",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil || len(result.RunIDs) != 1 {
		t.Fatalf("create retry source: %+v %v", result, err)
	}
	return result.RunIDs[0]
}

func TestRetryAgentRunDispatchesNewAttemptAndPreservesHistory(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			db := openDevRouteTestDB(t)
			ctx := t.Context()
			ids := store.DefaultDevFixtureIDs()
			s, _ := newDevRouteAuditStore(t, db)
			if _, err := s.SeedDevFixture(ctx); err != nil {
				t.Fatal(err)
			}
			memberID := "00000000-0000-0000-0000-000000000101"
			insertConversationMessageUser(t, db, ids, memberID, "retry-member@example.com", "member")
			broker := runstream.NewBroker(runstream.DefaultBufferSize)
			conn := &fakeStreamConnector{caps: connector.Capabilities{Sync: true, Streaming: true}, events: []connector.PromptEvent{
				{Type: connector.EventDelta, Delta: "retry-complete"},
				{Type: connector.EventDone, Final: &connector.PromptOutput{Content: "retry-complete"}},
			}}
			registry := testConnectorRegistry(t, conn)
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, s, WithRunStreamBroker(broker), WithConnectorRegistry(registry))
			oldID := createRetryTestRun(t, s, ids)
			if _, err := s.CompleteAgentRun(ctx, store.CompleteAgentRunInput{RunID: oldID, Content: "previous output"}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `update agent_runs set status=$2, failure_reason='original failure' where id=$1`, oldID, status); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordAgentRunEvent(ctx, store.RecordAgentRunEventInput{RunID: oldID, EventKind: "run.failed", Payload: map[string]any{"error": "original failure"}}); err != nil {
				t.Fatal(err)
			}
			broker.Publish(oldID, connector.PromptEvent{Type: connector.EventError, Error: "original failure"})
			broker.Finish(oldID)
			blockingID := ""
			if status == "cancelled" {
				blockingID = createRetryTestRun(t, s, ids)
				if _, err := db.Exec(ctx, `update agent_runs set status='running' where id=$1`, blockingID); err != nil {
					t.Fatal(err)
				}
			}
			var cancelRequest context.CancelFunc
			s.SetStreamingDispatcher(retryTestDispatcher(func(dispatchCtx context.Context, in store.StreamingDispatchInput) {
				cancelRequest()
				if dispatchCtx.Err() != nil {
					t.Error("committed dispatch inherited HTTP cancellation")
				}
				var clean bool
				if err := db.QueryRow(ctx, `select output_message_id is null and failure_reason='' and external_run_id='' from agent_runs where id=$1`, in.RunID).Scan(&clean); err != nil || !clean {
					t.Errorf("new run is not committed and clean: %v, %v", clean, err)
				}
				_, err := StartConversationRun(dispatchCtx, s, StreamingDispatchDeps{Broker: broker, ConnectorRegistry: registry, DispatchCtx: ctx}, in.RunID, in.ConversationID)
				if err != nil {
					t.Errorf("dispatch: %v", err)
				}
			}))
			path := "/api/v1/agent-runs/" + oldID + "/retry"
			request := newConversationMessageRequest(http.MethodPost, path, `{}`, memberID)
			// Preserve the authenticated context while allowing the post-commit hook to cancel it.
			requestCtx, cancelRequest := context.WithCancel(request.Context())
			defer cancelRequest()
			response := serveConversationMessageRequest(r, request.WithContext(requestCtx))
			if response.Code != http.StatusOK {
				t.Fatalf("retry = %d %s", response.Code, response.Body.String())
			}
			var result store.RetryAgentRunResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.RunID == oldID || result.RunID == "" || result.ConversationID != ids.ConversationID {
				t.Fatalf("replacement = %+v", result)
			}
			if blockingID != "" {
				waitForRunStatus(t, db, result.RunID, "queued")
				if _, err := s.CompleteAgentRun(ctx, store.CompleteAgentRunInput{RunID: blockingID, Content: "queue cleared"}); err != nil {
					t.Fatal(err)
				}
			}
			waitForRunStatus(t, db, result.RunID, "completed")
			var preserved, sameTrigger bool
			if err := db.QueryRow(ctx, `select old.status=$3 and old.failure_reason='original failure' and old.output_message_id<>fresh.output_message_id and old.finished_at is not null,
                old.trigger_message_id=fresh.trigger_message_id and fresh.requested_by_id=$4::uuid and fresh.trigger_source='manual'
                from agent_runs old join agent_runs fresh on fresh.id=$2 where old.id=$1`, oldID, result.RunID, status, memberID).Scan(&preserved, &sameTrigger); err != nil || !preserved || !sameTrigger {
				t.Fatalf("history/trigger/requester: %v %v %v", preserved, sameTrigger, err)
			}
			waitForRunEventKind(t, db, oldID, "run.failed")
			waitForRunEventKind(t, db, result.RunID, "run.completed")
			foundDone := false
			for event := range broker.Subscribe(ctx, result.RunID) {
				if event.Type == connector.EventDone && event.Final != nil && event.Final.Content == "retry-complete" {
					foundDone = true
				}
			}
			if !foundDone {
				t.Fatal("replacement stream lost its final result")
			}
			for range 2 {
				again := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{}`, memberID))
				var repeated store.RetryAgentRunResult
				if err := json.Unmarshal(again.Body.Bytes(), &repeated); err != nil || again.Code != http.StatusOK || repeated.RunID != result.RunID {
					t.Fatalf("repeated retry created another attempt: %d %s", again.Code, again.Body.String())
				}
			}
		})
	}
}

func TestRetryAgentRunGuardsAndConcurrentRequests(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := t.Context()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)
	oldID := createRetryTestRun(t, s, ids)
	path := "/api/v1/agent-runs/" + oldID + "/retry"
	for _, status := range []string{"queued", "running", "completed"} {
		if _, err := db.Exec(ctx, `update agent_runs set status=$2 where id=$1`, oldID, status); err != nil {
			t.Fatal(err)
		}
		res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{}`, ids.UserID))
		if res.Code != http.StatusConflict {
			t.Fatalf("%s accepted: %d %s", status, res.Code, res.Body.String())
		}
	}
	if _, err := db.Exec(ctx, `update agent_runs set status='failed' where id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	legacy := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, "/api/v1/agent-runs/"+oldID+"/requeue", `{}`, ids.UserID))
	var legacyResult store.RequeueAgentRunResult
	if err := json.Unmarshal(legacy.Body.Bytes(), &legacyResult); err != nil || legacy.Code != http.StatusOK || legacyResult.RunID != oldID {
		t.Fatalf("legacy requeue changed: %d %s", legacy.Code, legacy.Body.String())
	}
	if _, err := db.Exec(ctx, `update agent_runs set status='failed' where id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"viewer", ""} {
		userID := []string{"00000000-0000-0000-0000-000000000101", "00000000-0000-0000-0000-000000000102"}[i]
		insertConversationMessageUser(t, db, ids, userID, role+"-retry@example.com", role)
		res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{}`, userID))
		if res.Code != http.StatusForbidden && res.Code != http.StatusNotFound {
			t.Fatalf("role %q accepted: %d", role, res.Code)
		}
	}
	if _, err := db.Exec(ctx, `update agents set status='disabled' where id=$1`, ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	res := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{}`, ids.UserID))
	if res.Code != http.StatusConflict {
		t.Fatalf("disabled agent accepted: %d %s", res.Code, res.Body.String())
	}
	if _, err := db.Exec(ctx, `update agents set status='active' where id=$1`, ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	results := make(chan string, 4)
	for range 4 {
		go func() {
			response := serveConversationMessageRequest(r, newConversationMessageRequest(http.MethodPost, path, `{}`, ids.UserID))
			var result store.RetryAgentRunResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
				t.Errorf("concurrent retry: %d %s", response.Code, response.Body.String())
			}
			results <- result.RunID
		}()
	}
	first := <-results
	for range 3 {
		if next := <-results; first == "" || next != first {
			t.Fatalf("duplicate replacement: %s %s", first, next)
		}
	}
	if _, err := db.Exec(ctx, `update agents set connector_type='http' where id=$1`, ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	httpSourceID := createRetryTestRun(t, s, ids)
	if _, err := db.Exec(ctx, `update agent_runs set status='failed' where id=$1`, httpSourceID); err != nil {
		t.Fatal(err)
	}
	httpRetry, err := s.RetryAgentRun(ctx, store.RetryAgentRunInput{RunID: httpSourceID, UserID: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil || !claimed.Claimed || claimed.RunID != httpRetry.RunID {
		t.Fatalf("HTTP retry not claimable: %+v %v", claimed, err)
	}
}
