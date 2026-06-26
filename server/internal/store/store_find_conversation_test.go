package store

import (
	"context"
	"errors"
	"testing"
)

// TestFindConversationByExternalRefHitsRealColumns pins the regression
// from 2026-06-17. The original query referenced an "archived_at"
// column (does not exist) and filtered on metadata->>'gateway' /
// 'external_chat_id' / 'external_thread_id' even though the
// conversations table stores those as first-class columns. The first
// real caller (sharedbot /cancel branch) hit SQLSTATE 42703 in prod
// and the user got no response.
//
// This test seeds a conversation via the same ConfigureDevConversationExternalRef
// path the dev API uses, then asserts FindConversationByExternalRef
// resolves the (gateway, external_chat_id, external_thread_id) tuple
// back to the correct id — exercising both the columns and the
// deleted_at filter against a real PG schema, so a regression on
// either side fails the test instead of a prod user.
func TestFindConversationByExternalRefHitsRealColumns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID:   ids.ConversationID,
		Gateway:          "feishu",
		ExternalChatID:   "oc_test_chat",
		ExternalThreadID: "om_test_thread",
	}); err != nil {
		t.Fatalf("configure conv external ref: %v", err)
	}

	got, err := store.FindConversationByExternalRef(ctx, "feishu", "oc_test_chat", "om_test_thread")
	if err != nil {
		t.Fatalf("FindConversationByExternalRef: %v", err)
	}
	if got != ids.ConversationID {
		t.Fatalf("resolved conversation id = %q, want %q", got, ids.ConversationID)
	}

	// Empty thread_id should also resolve when the seeded conversation has none.
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID:   ids.ConversationID,
		Gateway:          "feishu",
		ExternalChatID:   "oc_topless_chat",
		ExternalThreadID: "",
	}); err != nil {
		t.Fatalf("configure conv (no thread): %v", err)
	}
	if got, err := store.FindConversationByExternalRef(ctx, "feishu", "oc_topless_chat", ""); err != nil {
		t.Fatalf("FindConversationByExternalRef (no thread): %v", err)
	} else if got != ids.ConversationID {
		t.Fatalf("resolved conversation id (no thread) = %q, want %q", got, ids.ConversationID)
	}

	// Wrong tuple → ErrUnknownConversation, not a SQL error.
	if _, err := store.FindConversationByExternalRef(ctx, "feishu", "oc_nonexistent", ""); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("expected ErrUnknownConversation for missing chat, got %v", err)
	}
}
