package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentStatusAuditRetainsRequestActor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s, ingester := newAuditAwareStore(t, db)
	ids := mustSeedDevFixture(t, ctx, s)
	for _, tc := range []struct {
		event, actorID, prev, next string
		mutate                     func(context.Context, string, string) (AgentStatusRead, error)
	}{
		{"agent.disabled", ids.UserID, "active", "disabled", s.DisableAgent},
		{"agent.enabled", "00000000-0000-0000-0000-0000000000aa", "disabled", "active", s.EnableAgent},
	} {
		t.Run(tc.event, func(t *testing.T) {
			if _, err := tc.mutate(ctx, ids.BackendAgentID, tc.actorID); err != nil {
				t.Fatal(err)
			}
			const unknownAgentID = "00000000-0000-0000-0000-000000000000"
			if _, err := tc.mutate(ctx, unknownAgentID, tc.actorID); !errors.Is(err, ErrUnknownAgent) {
				t.Fatalf("expected unknown Agent error, got %v", err)
			}
			flushAudit(t, ingester)
			records, err := s.ListAuditRecords(ctx, ListAuditRecordsFilter{EventType: tc.event}, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("expected one successful mutation event, got %+v", records)
			}
			record := records[0]
			if record.ActorType != "user" || record.ActorID != tc.actorID || record.Source != "admin" {
				t.Fatalf("unexpected audit identity: %+v", record)
			}
			if record.TargetType != "agent" || record.TargetID != ids.BackendAgentID || record.WorkspaceID != ids.WorkspaceID {
				t.Fatalf("unexpected audit target: %+v", record)
			}
			if record.Payload["prev"] != tc.prev || record.Payload["next"] != tc.next {
				t.Fatalf("unexpected status transition: %+v", record.Payload)
			}
		})
	}
}
