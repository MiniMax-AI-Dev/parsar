package store

import (
	"context"
	"errors"
	"testing"
)

// Round-trip test for the Feishu inbound reaction lookups against real
// Postgres. Both lookups read external_message_id and gateway out of
// messages.metadata jsonb, not off independent columns; a fake store
// would silently mask a SQL/column drift here.

func TestFeishuInboundReactionLookupsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := mustSeedDevFixture(t, ctx, store)
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID: ids.ConversationID,
		Gateway:        "feishu",
		ExternalChatID: "oc_reaction_test",
	}); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		SenderEmail:       "admin@example.com",
		Text:              "message that needs a reaction",
		Mentions:          []string{"@backend-agent"},
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalChatID:    "oc_reaction_test",
		ExternalMessageID: "om_reaction_target",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Before any RecordFeishuInboundReaction, ByConversation must return
	// ErrUnknownMessage so the dispatcher skips the DELETE call cleanly.
	if _, err := store.FindLatestFeishuInboundReactionByConversation(ctx, ids.ConversationID); !errors.Is(err, ErrUnknownMessage) {
		t.Fatalf("expected ErrUnknownMessage before reaction recorded, got %v", err)
	}

	if err := store.RecordFeishuInboundReaction(ctx, RecordFeishuInboundReactionInput{
		MessageID:  created.MessageID,
		ReactionID: "r-typing-1",
		AppID:      "cli_reaction_app",
		EmojiType:  "Typing",
	}); err != nil {
		t.Fatal(err)
	}

	byConv, err := store.FindLatestFeishuInboundReactionByConversation(ctx, ids.ConversationID)
	if err != nil {
		t.Fatalf("FindLatestFeishuInboundReactionByConversation: %v", err)
	}
	if byConv.MessageID != created.MessageID {
		t.Errorf("ByConversation MessageID = %q, want %q", byConv.MessageID, created.MessageID)
	}
	if byConv.ExternalMessageID != "om_reaction_target" {
		t.Errorf("ByConversation ExternalMessageID = %q, want om_reaction_target", byConv.ExternalMessageID)
	}
	if byConv.ReactionID != "r-typing-1" {
		t.Errorf("ByConversation ReactionID = %q, want r-typing-1", byConv.ReactionID)
	}
	if byConv.AppID != "cli_reaction_app" {
		t.Errorf("ByConversation AppID = %q, want cli_reaction_app", byConv.AppID)
	}

	byExternal, err := store.FindFeishuInboundReactionByExternalID(ctx, "om_reaction_target")
	if err != nil {
		t.Fatalf("FindFeishuInboundReactionByExternalID: %v", err)
	}
	if byExternal.MessageID != created.MessageID {
		t.Errorf("ByExternalID MessageID = %q, want %q", byExternal.MessageID, created.MessageID)
	}
	if byExternal.ReactionID != "r-typing-1" {
		t.Errorf("ByExternalID ReactionID = %q, want r-typing-1", byExternal.ReactionID)
	}
	if byExternal.AppID != "cli_reaction_app" {
		t.Errorf("ByExternalID AppID = %q, want cli_reaction_app", byExternal.AppID)
	}

	// After clear: ByConversation drops back to ErrUnknownMessage;
	// ByExternalID still finds the message row but with empty reaction
	// tuple (we only strip the gateway_reaction subtree).
	if err := store.ClearFeishuInboundReaction(ctx, created.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindLatestFeishuInboundReactionByConversation(ctx, ids.ConversationID); !errors.Is(err, ErrUnknownMessage) {
		t.Fatalf("expected ErrUnknownMessage after clear, got %v", err)
	}
	cleared, err := store.FindFeishuInboundReactionByExternalID(ctx, "om_reaction_target")
	if err != nil {
		t.Fatalf("FindFeishuInboundReactionByExternalID after clear: %v", err)
	}
	if cleared.ReactionID != "" || cleared.AppID != "" {
		t.Errorf("expected empty reaction tuple after clear, got reaction=%q app=%q", cleared.ReactionID, cleared.AppID)
	}
}
