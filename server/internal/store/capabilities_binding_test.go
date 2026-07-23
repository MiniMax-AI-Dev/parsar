package store

import (
	"context"
	"testing"
)

func TestEnableAgentCapabilityPersistsCredentialBindingAtomically(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := mustSeedDevFixture(t, ctx, st)

	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID,
		CreatorID:   ids.UserID,
		Type:        "mcp",
		Name:        "atomic-credential-binding",
		InitialVersion: &CreateCapabilityVersionInput{
			Version:   "1.0.0",
			CreatorID: ids.UserID,
			Content:   map[string]any{"mcpServers": map[string]any{"test": map[string]any{"command": "true"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}
	versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListCapabilityVersions: versions=%+v err=%v", versions, err)
	}
	agent, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Atomic Binding Agent",
		ConnectorType: "agent_daemon",
		AgentConfig:   map[string]any{"daemon_mode": "sandbox", "agent_kind": "opencode"},
		CreatedBy:     ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	secretID := "00000000-0000-0000-0000-000000000099"
	bindings := map[string]string{"notion_mcp_oauth": secretID}

	if _, err := st.EnableAgentCapability(ctx, agent.Agent.ID, versions[0].ID, nil, "invalid", bindings); err == nil {
		t.Fatal("EnableAgentCapability with invalid pinning mode unexpectedly succeeded")
	}
	afterFailure, err := st.GetAgent(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("GetAgent after rollback: %v", err)
	}
	if _, exists := afterFailure.Config["credential_bindings"]; exists {
		t.Fatalf("credential binding survived rolled-back enable: %+v", afterFailure.Config)
	}
	installed, err := st.ListAgentCapabilities(ctx, agent.Agent.ID)
	if err != nil || len(installed) != 0 {
		t.Fatalf("agent capability survived rolled-back enable: installed=%+v err=%v", installed, err)
	}

	if _, err := st.EnableAgentCapability(ctx, agent.Agent.ID, versions[0].ID, nil, PinningModePinned, bindings); err != nil {
		t.Fatalf("EnableAgentCapability: %v", err)
	}
	afterSuccess, err := st.GetAgent(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("GetAgent after success: %v", err)
	}
	storedBindings, ok := afterSuccess.Config["credential_bindings"].(map[string]any)
	if !ok {
		t.Fatalf("credential_bindings=%#v", afterSuccess.Config["credential_bindings"])
	}
	stored, ok := storedBindings["notion_mcp_oauth"].(map[string]any)
	if !ok || stored["source"] != "shared" || stored["secret_id"] != secretID {
		t.Fatalf("stored binding=%#v", storedBindings["notion_mcp_oauth"])
	}
	installed, err = st.ListAgentCapabilities(ctx, agent.Agent.ID)
	if err != nil || len(installed) != 1 || installed[0].CapabilityID != capability.ID {
		t.Fatalf("installed=%+v err=%v", installed, err)
	}
}
