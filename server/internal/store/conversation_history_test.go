package store

import (
	"context"
	"testing"
)

// The prompt path reads this on every turn for engines that cannot resume,
// so the contract is narrow: newest turns only, oldest-first, human and
// agent chat turns only.
func TestListRecentConversationHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := mustSeedDevFixture(t, ctx, store)

	send := func(content string) string {
		t.Helper()
		result, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
			ConversationID:    ids.ConversationID,
			UserID:            ids.UserID,
			Content:           content,
			MentionedAgentIDs: []string{ids.ProductAgentID},
		})
		if err != nil {
			t.Fatalf("send user message %q: %v", content, err)
		}
		if len(result.RunIDs) == 0 {
			t.Fatalf("expected a run for %q", content)
		}
		return result.RunIDs[0]
	}

	runID := send("@product-agent first question")
	if _, err := store.SendAssistantMessageFromRun(ctx, SendAssistantMessageFromRunInput{
		RunID:   runID,
		Source:  "agent",
		Content: "first answer",
	}); err != nil {
		t.Fatalf("send assistant message: %v", err)
	}
	secondRunID := send("@product-agent second question")

	// A runtime_error notice is a system message, not a conversation turn:
	// replaying it as history would teach the agent to answer Parsar's own
	// plumbing messages.
	if _, err := store.CreateRuntimeErrorSystemMessage(ctx, CreateRuntimeErrorSystemMessageInput{
		WorkspaceID:    ids.WorkspaceID,
		AgentID:        ids.ProductAgentID,
		RunID:          secondRunID,
		ConversationID: ids.ConversationID,
		SubKind:        "capability_credential_missing",
		CapabilityID:   "cap-1",
		CapabilityName: "MCP · github",
		CredentialKind: "github_token",
	}); err != nil {
		t.Fatalf("create runtime error system message: %v", err)
	}

	history, err := store.ListRecentConversationHistory(ctx, ids.ConversationID, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3 (2 user + 1 agent): %+v", len(history), history)
	}
	wantContents := []string{"@product-agent first question", "first answer", "@product-agent second question"}
	for i, want := range wantContents {
		if history[i].Content != want {
			t.Fatalf("history[%d].Content = %q, want %q (full: %+v)", i, history[i].Content, want, history)
		}
	}
	if history[0].SenderType != "user" || history[1].SenderType != "agent" {
		t.Fatalf("sender types = %q/%q, want user/agent", history[0].SenderType, history[1].SenderType)
	}
	if history[0].ID == "" || history[0].CreatedAt.IsZero() {
		t.Fatalf("history rows must carry id + created_at: %+v", history[0])
	}
	if history[0].CreatedAt.After(history[2].CreatedAt) {
		t.Fatalf("rows must be oldest-first: %+v", history)
	}

	// A limit keeps the tail, not the head: the newest turns are the ones
	// the next answer depends on.
	tail, err := store.ListRecentConversationHistory(ctx, ids.ConversationID, 2)
	if err != nil {
		t.Fatalf("list history with limit: %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("limited history length = %d, want 2", len(tail))
	}
	if tail[0].Content != "first answer" || tail[1].Content != "@product-agent second question" {
		t.Fatalf("limit must keep the newest turns oldest-first, got %+v", tail)
	}

	if zero, err := store.ListRecentConversationHistory(ctx, ids.ConversationID, 0); err != nil || zero != nil {
		t.Fatalf("non-positive limit must read nothing: rows=%+v err=%v", zero, err)
	}
}
