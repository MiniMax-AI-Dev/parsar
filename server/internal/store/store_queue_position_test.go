package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// seedRunsForQueuePosition inserts agent_runs directly so the lane shape
// (one running + N queued) bypasses SendUserMessageToConversation.
func seedRunsForQueuePosition(t *testing.T, store *Store, conversationID string, statuses []string) []string {
	t.Helper()
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	runIDs := make([]string, len(statuses))
	baseTime := time.Now().UTC().Add(-1 * time.Minute)
	for i, status := range statuses {
		id := newID()
		// Stagger created_at by 1ms: the SQL counts created_at <= target.created_at.
		createdAt := baseTime.Add(time.Duration(i) * time.Millisecond)
		if _, err := store.db.Exec(ctx, `
			insert into agent_runs (
			  id, workspace_id, project_id, conversation_id, project_agent_id,
			  connector_type, status, trigger_source, trigger_channel, requested_by_type, requested_by_id,
			  created_at, updated_at
			) values (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
			  'agent_daemon', $6, 'message', 'web', 'user', $7::uuid,
			  $8, $8
			)`,
			mustUUID(id), mustUUID(ids.WorkspaceID), mustUUID(ids.ProjectID),
			mustUUID(conversationID), mustUUID(ids.ProductProjectAgentID),
			status, mustUUID(ids.UserID), timestamptz(createdAt),
		); err != nil {
			t.Fatalf("seed run %d (%s): %v", i, status, err)
		}
		runIDs[i] = id
	}
	return runIDs
}

// TestQueuePositionForRun_RunningSiblingNotCountedAsAhead pins that a
// running lane-holder does not count as "ahead" of a queued sibling.
func TestQueuePositionForRun_RunningSiblingNotCountedAsAhead(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()
	runIDs := seedRunsForQueuePosition(t, store, ids.ConversationID, []string{
		"running",
		"queued",
		"queued",
	})

	cases := []struct {
		name string
		idx  int
		want int
	}{
		{"running lane-holder reports 1 (head of lane)", 0, 1},
		{"first queued: alone in queue → 第 1 位", 1, 1},
		{"second queued: one queued ahead → 第 2 位", 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.QueuePositionForRun(ctx, runIDs[tc.idx])
			if err != nil {
				t.Fatalf("QueuePositionForRun(%s): %v", runIDs[tc.idx], err)
			}
			if got != tc.want {
				t.Errorf("QueuePositionForRun(idx=%d, status=%s) = %d, want %d", tc.idx, tc.name, got, tc.want)
			}
		})
	}
}

// TestQueuePositionForRun_UnknownRunReturnsError keeps the
// ErrUnknownAgentRun contract so callers distinguish "alone in lane"
// (position=1) from "row not found".
func TestQueuePositionForRun_UnknownRunReturnsError(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	bogus := newID()
	_, err := store.QueuePositionForRun(ctx, bogus)
	if !errors.Is(err, ErrUnknownAgentRun) {
		t.Errorf("QueuePositionForRun(unknown) err = %v, want ErrUnknownAgentRun", err)
	}
}
