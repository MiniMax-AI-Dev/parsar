package store

// Regression for ListActiveFeishuInflightConversations scanning NULL
// output_message_id: any feishu run that hadn't yet stamped its
// output_message_id triggered a pgx scan failure of the form:
//
//   can't scan into dest[11] (col: output_message_id):
//   cannot scan NULL into *string
//
// The fix is `coalesce(r.output_message_id::text, '')` in store.sql.
// Needs real Postgres because mocks can't model NULL/scan semantics.

import (
	"context"
	"testing"
	"time"
)

func TestListActiveFeishuInflightConversations_NullOutputMessageIDDoesNotPanic(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := mustSeedDevFixture(t, ctx, store)

	// Wire the dev conversation to a feishu external chat so it
	// matches `where c.platform = 'feishu'`; without this the row would
	// be filtered out before the scan happened, masking the bug.
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID: ids.ConversationID,
		Gateway:        "feishu",
		ExternalChatID: "oc_nullscan_test",
	}); err != nil {
		t.Fatalf("configure conv external ref: %v", err)
	}

	// Spawn an agent run; output_message_id stays NULL until
	// CompleteAgentRun fires — the window the inflight driver lives in.
	sendRes, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent nullscan-test",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatalf("send user msg: %v", err)
	}
	if len(sendRes.RunIDs) == 0 {
		t.Fatal("expected at least one run id from SendUserMessageToConversation")
	}
	runID := sendRes.RunIDs[0]

	// At least one event so the seq_emitted < max_seq predicate is true;
	// without it the row is filtered before reaching the failing scan.
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "tool.call",
		Payload:    map[string]any{"name": "Bash", "args": map[string]any{"command": "echo hi"}},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	// Pre-fix this fails with `cannot scan NULL into *string` on dest[11].
	rows, err := store.ListActiveFeishuInflightConversations(ctx, time.Now().UTC().Add(-time.Hour), 32)
	if err != nil {
		t.Fatalf("ListActiveFeishuInflightConversations should succeed with NULL output_message_id, got: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			if r.OutputMessageID != "" {
				t.Errorf("OutputMessageID = %q, want '' (NULL coalesced)", r.OutputMessageID)
			}
			break
		}
	}
	if !found {
		t.Errorf("seeded run %s not returned; got %d rows", runID, len(rows))
	}
}
