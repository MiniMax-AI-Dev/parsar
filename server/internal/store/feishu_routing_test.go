package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGetAgentByFeishuAppID_HappyPath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	const appID = "cli_test_router_app_123"
	cfg := map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled":                true,
				"app_id":                 appID,
				"app_secret_ref":         "secret_app_secret_id",
				"verification_token_ref": "secret_verify_token_id",
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set config = $1::jsonb where id = $2::uuid`, cfgJSON, ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}

	route, err := New(db).GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		t.Fatalf("GetAgentByFeishuAppID: %v", err)
	}
	if route.AgentID != ids.ProductAgentID {
		t.Errorf("AgentID = %q, want %q", route.AgentID, ids.ProductAgentID)
	}
	if route.WorkspaceID != ids.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", route.WorkspaceID, ids.WorkspaceID)
	}
	if route.WorkspaceName == "" {
		t.Errorf("WorkspaceName must be populated for visibility-gate rejection text")
	}
	if route.Visibility != "workspace" {
		t.Errorf("Visibility default = %q, want %q", route.Visibility, "workspace")
	}
	if len(route.Config) == 0 {
		t.Errorf("Config bytes must be returned so caller can decode credential refs")
	}
}

// TestGetAgentByFeishuAppID_DisabledAgentInvisible: disabled /
// soft-deleted / disabled-connector rows must be excluded so old
// credentials stop working once an admin turns the Bot off.
func TestGetAgentByFeishuAppID_DisabledAgentInvisible(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	const appID = "cli_disabled_test"
	cfg := map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled": true,
				"app_id":  appID,
			},
		},
	}
	cfgJSON, _ := json.Marshal(cfg)

	// Case 1: connector disabled
	cfgDisabled := map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled": false,
				"app_id":  appID,
			},
		},
	}
	cfgDisabledJSON, _ := json.Marshal(cfgDisabled)
	if _, err := db.Exec(ctx, `update agents set config = $1::jsonb where id = $2::uuid`, cfgDisabledJSON, ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db).GetAgentByFeishuAppID(ctx, appID); !errors.Is(err, ErrUnknownFeishuAgent) {
		t.Errorf("disabled connector should be invisible; got %v", err)
	}

	// Case 2: agent status='disabled'
	if _, err := db.Exec(ctx, `update agents set config = $1::jsonb, status = 'disabled' where id = $2::uuid`, cfgJSON, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db).GetAgentByFeishuAppID(ctx, appID); !errors.Is(err, ErrUnknownFeishuAgent) {
		t.Errorf("disabled agent should be invisible; got %v", err)
	}

	// Case 3: soft-deleted agent
	if _, err := db.Exec(ctx, `update agents set config = $1::jsonb, status = 'active', deleted_at = now() where id = $2::uuid`, cfgJSON, ids.TestAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db).GetAgentByFeishuAppID(ctx, appID); !errors.Is(err, ErrUnknownFeishuAgent) {
		t.Errorf("soft-deleted agent should be invisible; got %v", err)
	}
}

// TestGetAgentByFeishuAppID_EmptyAppIDFastFail: an empty app_id from a
// malformed webhook envelope must not trigger a DB scan.
func TestGetAgentByFeishuAppID_EmptyAppIDFastFail(t *testing.T) {
	db := openTestDB(t)
	if _, err := New(db).GetAgentByFeishuAppID(context.Background(), "   "); !errors.Is(err, ErrUnknownFeishuAgent) {
		t.Fatalf("expected ErrUnknownFeishuAgent on empty app_id, got %v", err)
	}
}

func TestFindUserIDByPlatformSubject_HappyPath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	userID, err := New(db).FindUserIDByPlatformSubject(ctx, "feishu", "ou_feishu_admin")
	if err != nil {
		t.Fatalf("FindUserIDByPlatformSubject: %v", err)
	}
	if userID != ids.UserID {
		t.Errorf("user_id = %q, want %q", userID, ids.UserID)
	}
}

// TestFindUserIDByPlatformSubject_UnknownReturnsTypedError lets the
// visibility gate branch on ErrUnknownPlatformUser.
func TestFindUserIDByPlatformSubject_UnknownReturnsTypedError(t *testing.T) {
	db := openTestDB(t)
	_, err := New(db).FindUserIDByPlatformSubject(context.Background(), "feishu", "ou_does_not_exist_anywhere")
	if !errors.Is(err, ErrUnknownPlatformUser) {
		t.Fatalf("expected ErrUnknownPlatformUser, got %v", err)
	}
	if !strings.Contains(err.Error(), "ou_does_not_exist_anywhere") {
		t.Errorf("error must echo subject for debug; got %q", err)
	}
}

// TestFindUserIDByPlatformSubject_OtherProviderIgnored: a subject linked
// under one provider must be invisible to a lookup keyed on another.
func TestFindUserIDByPlatformSubject_OtherProviderIgnored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	const subject = "ou_collision_subject"
	if _, err := db.Exec(ctx, `
		insert into auth_identities(id, user_id, provider, subject, metadata, created_at, updated_at)
		values (gen_random_uuid(), $1::uuid, 'oidc', $2, '{}', now(), now())
		on conflict do nothing
	`, ids.UserID, subject); err != nil {
		t.Fatal(err)
	}

	if _, err := New(db).FindUserIDByPlatformSubject(ctx, "feishu", subject); !errors.Is(err, ErrUnknownPlatformUser) {
		t.Errorf("expected oidc-only subject to be invisible to feishu lookup, got %v", err)
	}
}

func TestGetAgentByID_HappyPath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	route, err := st.GetAgentByID(ctx, ids.BackendAgentID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if route.AgentID != ids.BackendAgentID || route.WorkspaceID != ids.WorkspaceID || route.AgentSlug != "backend-agent" {
		t.Fatalf("unexpected route: %+v", route)
	}

	if _, err := db.Exec(ctx, `update agents set status = 'disabled' where id = $1::uuid`, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAgentByID(ctx, ids.BackendAgentID); !errors.Is(err, ErrUnknownFeishuAgent) {
		t.Fatalf("disabled selected agent should be unknown, got %v", err)
	}
}

func TestGatewaySessionSelectionRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	_, err := st.GetGatewaySessionSelection(ctx, "feishu", "oc_chat", "")
	if !errors.Is(err, ErrUnknownGatewaySessionSelection) {
		t.Fatalf("initial selection err = %v, want ErrUnknownGatewaySessionSelection", err)
	}

	if err := st.UpsertGatewaySessionSelection(ctx, GatewaySessionSelectionInput{
		Platform:   "feishu",
		ExternalID: "oc_chat",
		AgentID:    ids.ProductAgentID,
		Metadata: map[string]any{
			"host_app_id": "cli_shared",
		},
	}); err != nil {
		t.Fatalf("first UpsertGatewaySessionSelection: %v", err)
	}
	got, err := st.GetGatewaySessionSelection(ctx, "feishu", "oc_chat", "")
	if err != nil {
		t.Fatalf("GetGatewaySessionSelection after first upsert: %v", err)
	}
	if got != ids.ProductAgentID {
		t.Fatalf("selected agent = %q, want %q", got, ids.ProductAgentID)
	}

	if err := st.UpsertGatewaySessionSelection(ctx, GatewaySessionSelectionInput{
		Platform:   "feishu",
		ExternalID: "oc_chat",
		AgentID:    ids.BackendAgentID,
	}); err != nil {
		t.Fatalf("second UpsertGatewaySessionSelection: %v", err)
	}
	got, err = st.GetGatewaySessionSelection(ctx, "feishu", "oc_chat", "")
	if err != nil {
		t.Fatalf("GetGatewaySessionSelection after second upsert: %v", err)
	}
	if got != ids.BackendAgentID {
		t.Fatalf("selected agent after overwrite = %q, want %q", got, ids.BackendAgentID)
	}

	_, err = st.GetGatewaySessionSelection(ctx, "slack", "oc_chat", "")
	if !errors.Is(err, ErrUnknownGatewaySessionSelection) {
		t.Fatalf("other platform err = %v, want ErrUnknownGatewaySessionSelection", err)
	}
}

func TestListFeishuSharedBotAgentsRespectsVisibility(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set visibility = 'tenant' where id = $1::uuid`, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set visibility = 'public' where id = $1::uuid`, ids.TestAgentID); err != nil {
		t.Fatal(err)
	}

	registered, err := st.ListFeishuSharedBotAgents(ctx, ids.WorkspaceID, ids.UserID, ids.ProductAgentID, 20)
	if err != nil {
		t.Fatalf("ListFeishuSharedBotAgents registered: %v", err)
	}
	registeredIDs := feishuSharedAgentIDs(registered)
	if registeredIDs[ids.ProductAgentID] {
		t.Fatalf("host agent should be excluded: %+v", registered)
	}
	if !registeredIDs[ids.BackendAgentID] || !registeredIDs[ids.TestAgentID] {
		t.Fatalf("registered workspace member should see backend+test, got %+v", registered)
	}

	otherWorkspace, err := st.ListFeishuSharedBotAgents(ctx, "00000000-0000-0000-0000-000000000099", ids.UserID, ids.ProductAgentID, 20)
	if err != nil {
		t.Fatalf("ListFeishuSharedBotAgents other workspace: %v", err)
	}
	if len(otherWorkspace) != 0 {
		t.Fatalf("agent list leaked rows outside the requested workspace: %+v", otherWorkspace)
	}

	guest, err := st.ListFeishuSharedBotAgents(ctx, ids.WorkspaceID, "", ids.ProductAgentID, 20)
	if err != nil {
		t.Fatalf("ListFeishuSharedBotAgents guest: %v", err)
	}
	guestIDs := feishuSharedAgentIDs(guest)
	if len(guestIDs) != 1 || !guestIDs[ids.TestAgentID] {
		t.Fatalf("guest should only see public test agent, got %+v", guest)
	}
}

func TestListFeishuSharedBotAgentsExcludesDedicatedBotBindings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set visibility = 'tenant' where id = $1::uuid`, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:              ids.BackendAgentID,
		Enabled:              true,
		AppID:                "cli_backend_dedicated",
		AppSecretRef:         "secret_app",
		VerificationTokenRef: "secret_verify",
		EventMode:            "webhook",
		RoutingMode:          "direct",
	}, ids.UserID); err != nil {
		t.Fatalf("UpdateAgentFeishuConnector direct: %v", err)
	}

	agents, err := st.ListFeishuSharedBotAgents(ctx, ids.WorkspaceID, ids.UserID, ids.ProductAgentID, 20)
	if err != nil {
		t.Fatalf("ListFeishuSharedBotAgents: %v", err)
	}
	if got := feishuSharedAgentIDs(agents); got[ids.BackendAgentID] {
		t.Fatalf("dedicated Bot Agent should be removed from default Bot candidates, got %+v", agents)
	}
}

func feishuSharedAgentIDs(agents []FeishuSharedBotAgent) map[string]bool {
	out := make(map[string]bool, len(agents))
	for _, agent := range agents {
		out[agent.AgentID] = true
	}
	return out
}
