package store

import (
	"context"
	"testing"
)

func TestHTTPAgentAutomaticallyDispatchesSendRetryAndQueuedSuccessor(t *testing.T) {
	ctx := context.Background()
	st := New(openTestDB(t))
	ids := mustSeedDevFixture(t, ctx, st)
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID: ids.WorkspaceID, CreatedBy: ids.UserID,
		Name: "HTTP dispatch", ConnectorType: "http",
		AgentConfig: map[string]any{"http": map[string]any{"endpoint": "https://agent.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID, Title: "HTTP dispatch", PrimaryAgentID: created.Agent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingStreamingDispatcher{}
	st.SetStreamingDispatcher(recorder)
	assertDispatched := func(count int, runID string) {
		t.Helper()
		calls := recorder.Snapshot()
		if len(calls) != count || calls[count-1] != (StreamingDispatchInput{
			RunID: runID, ConversationID: conv.ID, ConnectorType: "http",
		}) {
			t.Fatalf("dispatch calls = %+v, want %d calls ending with run %s", calls, count, runID)
		}
	}
	send := func(content string) string {
		t.Helper()
		result, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
			ConversationID: conv.ID, UserID: ids.UserID, Content: content,
		})
		if err != nil || len(result.RunIDs) != 1 {
			t.Fatalf("send: %v, runs: %v", err, result.RunIDs)
		}
		return result.RunIDs[0]
	}
	first := send("first")
	assertDispatched(1, first)
	if _, err := st.MarkAgentRunRunning(ctx, first, conv.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.FailAgentRun(ctx, FailAgentRunInput{RunID: first, Source: "http", Reason: "unavailable"}); err != nil {
		t.Fatal(err)
	}
	retry, err := st.RetryAgentRun(ctx, RetryAgentRunInput{RunID: first, UserID: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	assertDispatched(2, retry.RunID)
	if _, err := st.MarkAgentRunRunning(ctx, retry.RunID, conv.ID); err != nil {
		t.Fatal(err)
	}
	next := send("next")
	assertDispatched(3, next)
	if _, err := st.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: retry.RunID, Source: "http", Content: "done"}); err != nil {
		t.Fatal(err)
	}
	assertDispatched(4, next)
}
