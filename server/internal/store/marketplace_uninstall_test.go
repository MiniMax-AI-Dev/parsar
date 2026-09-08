package store

import (
	"context"
	"testing"
)

func TestMarketplaceUninstallListsAffectedAgentsAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	st := New(openTestDB(t))
	ids := mustSeedDevFixture(t, ctx, st)
	source, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Marketplace source", CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: source.Workspace.ID, CreatorID: ids.UserID,
		Type: "mcp", Name: "Shared test tool", Visibility: "public",
		InitialVersion: &CreateCapabilityVersionInput{Version: "1.0.0", CreatorID: ids.UserID, Content: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions = %+v, error = %v", versions, err)
	}
	versionID := versions[0].ID
	sourceAgent, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID: source.Workspace.ID, Name: "Source agent", ConnectorType: "agent_daemon", CreatedBy: ids.UserID,
		AgentConfig:         map[string]any{"agent_kind": "claude_code", "daemon_mode": "sandbox"},
		InitialCapabilities: []InitialAgentCapabilityInput{{CapabilityVersionID: versionID, PinningMode: PinningModePinned}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ids.ProductAgentID, ids.BackendAgentID} {
		if _, err := st.EnableAgentCapability(ctx, id, versionID, nil, PinningModePinned); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ListEnabledAgents(ctx, ids.WorkspaceID, capability.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].AgentID != ids.BackendAgentID || rows[1].AgentID != ids.ProductAgentID {
		t.Fatalf("affected agents = %+v, want target workspace agents in name order", rows)
	}
	for _, row := range rows {
		if row.AgentName == "" || !row.Enabled || row.CapabilityVersionID != versionID || row.Version != "1.0.0" {
			t.Fatalf("incomplete affected agent: %+v", row)
		}
	}
	removed, err := st.UninstallWorkspaceMarketplaceCapability(ctx, ids.WorkspaceID, capability.ID)
	if err != nil || removed != 2 {
		t.Fatalf("removed = %d, error = %v", removed, err)
	}
	rows, err = st.ListEnabledAgents(ctx, ids.WorkspaceID, capability.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("remaining target bindings = %+v, error = %v", rows, err)
	}
	rows, err = st.ListEnabledAgents(ctx, source.Workspace.ID, capability.ID)
	if err != nil || len(rows) != 1 || rows[0].AgentID != sourceAgent.Agent.ID {
		t.Fatalf("source binding changed: %+v, error = %v", rows, err)
	}
	for _, id := range []string{ids.BackendAgentID, ids.ProductAgentID} {
		agent, err := st.GetAgent(ctx, id)
		if err != nil || agent.Status != "active" {
			t.Fatalf("target agent changed: %+v, error = %v", agent, err)
		}
	}
}
