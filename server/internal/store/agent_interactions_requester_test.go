package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAgentInteractionRequesterMetadata(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := mustSeedDevFixture(t, ctx, s)
	memberID := "e536db30-5f14-437d-bdd7-4f84fb816ec1"
	if _, err := db.Exec(ctx, `insert into users(id,email,name,created_at,updated_at) values($1,'requester@example.test','QA Member',now(),now())`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `insert into workspace_members(id,workspace_id,user_id,role,created_at,updated_at) values(gen_random_uuid(),$1,$2,'member',now(),now())`, ids.WorkspaceID, memberID); err != nil {
		t.Fatal(err)
	}
	otherWorkspaceID := "e536db30-5f14-437d-bdd7-4f84fb816ec2"
	if _, err := db.Exec(ctx, `insert into workspaces(id,name,slug,created_at,updated_at) values($1,'Other requester workspace','other-requester-workspace',now(),now())`, otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `insert into workspace_members(id,workspace_id,user_id,role,created_at,updated_at) values(gen_random_uuid(),$1,$2,'member',now(),now())`, otherWorkspaceID, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update users set name='QA Owner' where id=$1`, ids.UserID); err != nil {
		t.Fatal(err)
	}
	requestIDs := map[string]string{}
	for i, userID := range []string{ids.UserID, memberID} {
		result, err := s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
			ConversationID: ids.ConversationID, UserID: userID, Content: "check the same tool",
			MentionedAgentIDs: []string{ids.ProductAgentID},
		})
		if err != nil || len(result.RunIDs) != 1 {
			t.Fatalf("create requester run: %v, %v", result, err)
		}
		runID := result.RunIDs[0]
		requestIDs[runID] = fmt.Sprintf("requester-%d", i)
		if err := s.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
			RunID: runID, EventKind: "permission.asked", OccurredAt: time.Now().UTC(),
			Payload: map[string]any{"request_id": requestIDs[runID], "action": "mcp__service__get_ticket"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListWorkspaceAgentInteractions(ctx, ids.WorkspaceID, "pending", 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list two requesters: %v, %v", rows, err)
	}
	var member AgentInteractionRead
	for _, row := range rows {
		want := "QA Owner"
		if row.RequestedByID == memberID {
			want = "QA Member"
			member = row
		} else if row.RequestedByID != ids.UserID {
			t.Fatalf("unexpected requester ID: %q", row.RequestedByID)
		}
		if row.RequestedByType != "user" || row.RequestedByName != want {
			t.Fatalf("wrong requester metadata: %+v", row)
		}
	}
	assertReads := func(t *testing.T, wantType, wantName string) {
		t.Helper()
		byID, err := s.GetAgentInteraction(ctx, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		byRequest, err := s.GetAgentInteractionByRequestID(ctx, "permission", requestIDs[member.AgentRunID], member.AgentRunID)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := s.ListWorkspaceAgentInteractions(ctx, ids.WorkspaceID, "pending", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range append(listed, byID, byRequest) {
			if row.ID != member.ID {
				continue
			}
			if row.RequestedByType != wantType || row.RequestedByID != memberID || row.RequestedByName != wantName {
				t.Fatalf("requester read mismatch: %+v", row)
			}
			if row.Request["action"] != "mcp__service__get_ticket" || row.Status != "pending" {
				t.Fatalf("request behavior changed: %+v", row)
			}
		}
	}
	assertReads(t, "user", "QA Member")
	cases := []struct{ name, query, wantType, wantName string }{
		{"email fallback", `update users set name=' ' where id=$1`, "user", "requester@example.test"},
		{"non-user ID collision", `update agent_runs set requested_by_type='agent' where requested_by_id=$1`, "agent", ""},
		{"restore user identity", `update agent_runs set requested_by_type='user' where requested_by_id=$1`, "user", "requester@example.test"},
		{"pending membership", `update workspace_members set status='pending' where user_id=$1 and workspace_id in (select workspace_id from agent_runs where requested_by_id=$1)`, "user", ""},
		{"restore membership", `update workspace_members set status='active' where user_id=$1`, "user", "requester@example.test"},
		{"deleted account", `update users set deleted_at=now() where id=$1`, "user", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, tc.query, memberID); err != nil {
				t.Fatal(err)
			}
			assertReads(t, tc.wantType, tc.wantName)
		})
	}
}
