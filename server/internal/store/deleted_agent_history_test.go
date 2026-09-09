package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeletedAgentRetainsReadOnlyHistory(t *testing.T) {
	ctx := context.Background()
	st := New(openTestDB(t))
	ids := mustSeedDevFixture(t, ctx, st)
	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID, Title: "Retained history", PrimaryAgentID: ids.ProductAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func() string {
		t.Helper()
		result, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
			ConversationID: conv.ID, UserID: ids.UserID, Content: "History fixture",
		})
		if err != nil || len(result.RunIDs) != 1 {
			t.Fatalf("send: runs=%v error=%v", result.RunIDs, err)
		}
		return result.RunIDs[0]
	}
	completedID := send()
	if _, err := st.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: completedID, Content: "Retained answer"}); err != nil {
		t.Fatal(err)
	}
	failedID := send()
	fail := func() {
		t.Helper()
		if err := st.FailAgentRun(ctx, FailAgentRunInput{RunID: failedID, Reason: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	fail()
	if _, err := st.RequeueFailedAgentRun(ctx, RequeueAgentRunInput{RunID: failedID}); err != nil {
		t.Fatalf("live Agent retry: %v", err)
	}
	fail()
	if _, _, err := st.DeleteAgent(ctx, ids.ProductAgentID, ids.UserID); err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{completedID, failedID} {
		run, err := st.GetAgentRun(ctx, runID)
		if err != nil || !run.AgentDeleted || run.AgentName != conv.PrimaryAgentName {
			t.Fatalf("retained run %s: %+v, %v", runID, run.AgentRunBriefRead, err)
		}
		if runID == completedID && (run.OutputMessage == nil || run.OutputMessage.Content != "Retained answer") {
			t.Fatal("completed output was not retained")
		}
	}
	page, err := st.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 1, 0, conv.PrimaryAgentName)
	if err != nil || page.Total != 2 || len(page.Runs) != 1 || !page.Runs[0].AgentDeleted {
		t.Fatalf("retained search and pagination: %+v, %v", page, err)
	}
	history, err := st.GetConversation(ctx, conv.ID)
	if err != nil || !history.PrimaryAgentDeleted || history.PrimaryAgentID != ids.ProductAgentID || history.PrimaryAgentName != conv.PrimaryAgentName {
		t.Fatalf("retained conversation identity: %+v, %v", history, err)
	}
	timeline, err := st.GetConversationTimeline(ctx, conv.ID, 100)
	if err != nil || len(timeline.AgentRuns) != 2 || len(timeline.Messages) != 3 {
		t.Fatalf("retained timeline: runs=%d messages=%d error=%v", len(timeline.AgentRuns), len(timeline.Messages), err)
	}
	if _, err := st.RequeueFailedAgentRun(ctx, RequeueAgentRunInput{RunID: failedID}); !errors.Is(err, ErrAgentRunNotCompletable) {
		t.Fatalf("deleted Agent retry must be rejected: %v", err)
	}
	run, err := st.GetAgentRun(ctx, failedID)
	if err != nil || run.Status != "failed" {
		t.Fatalf("rejected retry changed history: status=%s error=%v", run.Status, err)
	}
	sent, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: conv.ID, UserID: ids.UserID, Content: "Must not execute the deleted Agent",
	})
	if err != nil || len(sent.RunIDs) != 0 {
		t.Fatalf("deleted Agent received a new run: %v, %v", sent.RunIDs, err)
	}
	other, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Other history workspace", CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	page, err = st.ListWorkspaceAgentRuns(ctx, other.Workspace.ID, nil, 20, 0, completedID)
	if err != nil || page.Total != 0 || len(page.Runs) != 0 {
		t.Fatalf("history crossed workspaces: %+v, %v", page, err)
	}
	if err := st.SoftDeleteConversation(ctx, conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAgentRun(ctx, completedID); !errors.Is(err, ErrUnknownAgentRun) {
		t.Fatalf("deleted conversation must remain hidden: %v", err)
	}
	page, err = st.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 20, 0, conv.PrimaryAgentName)
	if err != nil || page.Total != 0 {
		t.Fatalf("deleted conversation remained in history: %+v, %v", page, err)
	}
}
