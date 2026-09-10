package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgentMCPTokenLifecycleAndCurrentPermissions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := mustSeedDevFixture(t, ctx, s)
	id := AgentMCPIdentity{AgentID: ids.ProductAgentID, UserID: ids.UserID, WorkspaceID: ids.WorkspaceID}
	now := time.Now().UTC()
	token := AgentMCPToken{CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.PutAgentMCPToken(ctx, id, "first-digest", token); err != nil {
		t.Fatal(err)
	}
	resolve := func(hash string, when time.Time, want bool) {
		t.Helper()
		got, found, err := s.ResolveAgentMCPToken(ctx, hash, when)
		if err != nil || found != want {
			t.Fatalf("resolve: found=%v error=%v", found, err)
		}
		if found && (got.AgentID != id.AgentID || got.UserID != id.UserID || got.WorkspaceID != id.WorkspaceID) {
			t.Fatalf("identity mismatch: %+v", got)
		}
	}
	resolve("first-digest", now, true)
	resolve("unknown", now, false)
	resolve("first-digest", token.ExpiresAt, false)
	if err := s.PutAgentMCPToken(ctx, id, "second-digest", token); err != nil {
		t.Fatal(err)
	}
	resolve("first-digest", now, false)
	resolve("second-digest", now, true)
	metadata, err := s.GetAgentMCPToken(ctx, id.AgentID, id.UserID)
	if err != nil || metadata == nil || metadata.ExpiresAt.Sub(token.ExpiresAt) > time.Millisecond {
		t.Fatalf("metadata: %+v %v", metadata, err)
	}
	for _, tc := range []struct{ name, disable, restore string }{
		{"viewer", `UPDATE workspace_members SET role='viewer'`, `UPDATE workspace_members SET role='owner'`},
		{"removed member", `UPDATE workspace_members SET deleted_at=now()`, `UPDATE workspace_members SET deleted_at=NULL`},
		{"pending member", `UPDATE workspace_members SET status='pending'`, `UPDATE workspace_members SET status='active'`},
		{"disabled user", `UPDATE users SET status='disabled'`, `UPDATE users SET status='active'`},
		{"deleted user", `UPDATE users SET deleted_at=now()`, `UPDATE users SET deleted_at=NULL`},
		{"deleted workspace", `UPDATE workspaces SET deleted_at=now()`, `UPDATE workspaces SET deleted_at=NULL`},
		{"disabled Agent", `UPDATE agents SET status='disabled'`, `UPDATE agents SET status='active'`},
		{"deleted Agent", `UPDATE agents SET deleted_at=now()`, `UPDATE agents SET deleted_at=NULL`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, tc.disable); err != nil {
				t.Fatal(err)
			}
			resolve("second-digest", now, false)
			if _, err := db.Exec(ctx, tc.restore); err != nil {
				t.Fatal(err)
			}
			resolve("second-digest", now, true)
		})
	}
	if err := s.DeleteAgentMCPToken(ctx, id); err != nil {
		t.Fatal(err)
	}
	resolve("second-digest", now, false)
	metadata, err = s.GetAgentMCPToken(ctx, id.AgentID, id.UserID)
	if err != nil || metadata != nil {
		t.Fatalf("revocation metadata: %+v %v", metadata, err)
	}
}

func TestAgentMCPTokenRotationAndRevocationArePersonal(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := mustSeedDevFixture(t, ctx, s)
	otherUser := "00000000-0000-0000-0000-000000000098"
	if _, err := db.Exec(ctx, `INSERT INTO users(id,email,created_at,updated_at) VALUES($1,'other-mcp@example.test',now(),now());`, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO workspace_members(id,user_id,workspace_id,role,created_at,updated_at) VALUES('00000000-0000-0000-0000-000000000097',$1,$2,'member',now(),now())`, otherUser, ids.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token := AgentMCPToken{CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	identities := []AgentMCPIdentity{
		{AgentID: ids.ProductAgentID, UserID: ids.UserID, WorkspaceID: ids.WorkspaceID},
		{AgentID: ids.ProductAgentID, UserID: otherUser, WorkspaceID: ids.WorkspaceID},
		{AgentID: ids.BackendAgentID, UserID: ids.UserID, WorkspaceID: ids.WorkspaceID},
	}
	hashes := []string{"first", "other-user", "other-agent"}
	for i, id := range identities {
		if err := s.PutAgentMCPToken(ctx, id, hashes[i], token); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteAgentMCPToken(ctx, identities[0]); err != nil {
		t.Fatal(err)
	}
	for i, hash := range hashes {
		_, found, err := s.ResolveAgentMCPToken(ctx, hash, now)
		if err != nil || found != (i > 0) {
			t.Fatalf("revoke affected %s: found=%v err=%v", hash, found, err)
		}
	}
}

func TestConversationMCPSourceKeepsUserAndTarget(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := mustSeedDevFixture(t, ctx, s)
	conversation, err := s.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, PrimaryAgentID: ids.ProductAgentID, Surface: "api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"mcp", ""} {
		result, err := s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: conversation.ID, UserID: ids.UserID, Content: "Only ask the bound Agent, even with @backend mentioned", MentionedAgentIDs: []string{ids.ProductAgentID}, Source: source})
		if err != nil || len(result.RunIDs) != 1 {
			t.Fatalf("send: %+v %v", result, err)
		}
		wantSource := source
		if wantSource == "" {
			wantSource = "web"
		}
		var channel, actor, agent, metadataSource string
		if err := db.QueryRow(ctx, `SELECT trigger_channel,requested_by_id::text,agent_id::text,metadata->>'source' FROM agent_runs WHERE id=$1`, result.RunIDs[0]).Scan(&channel, &actor, &agent, &metadataSource); err != nil {
			t.Fatal(err)
		}
		if channel != wantSource || metadataSource != wantSource || result.Message.Metadata["source"] != wantSource || actor != ids.UserID || agent != ids.ProductAgentID {
			t.Fatalf("wrong dispatch source or scope: %s %s %s %s", channel, actor, agent, metadataSource)
		}
	}
	_, err = s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: conversation.ID, UserID: ids.UserID, Content: "test", Source: "untrusted"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid source: %v", err)
	}
}
