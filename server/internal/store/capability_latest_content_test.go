package store

import (
	"encoding/json"
	"testing"
)

func TestEnabledCapabilityLatestContent(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	mustSeedDevFixture(t, ctx, st)
	content := func(command string) map[string]any {
		return map[string]any{"mcpServers": map[string]any{"versioned": map[string]any{"command": command}}}
	}
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID, CreatorID: ids.UserID, Type: "mcp", Name: "latest-content",
		InitialVersion: &CreateCapabilityVersionInput{Version: "1.0.0", CreatorID: ids.UserID, Content: content("v1"), RequiredCredentials: []RequiredCredential{{Kind: "github_pat", Required: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("initial versions = %+v: %v", versions, err)
	}
	v2, err := st.CreateCapabilityVersion(ctx, CreateCapabilityVersionInput{
		CapabilityID: capability.ID, CreatorID: ids.UserID, Version: "2.0.0", Content: content("v2"), RequiredCredentials: []RequiredCredential{{Kind: "slack_bot_token", Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID: ids.WorkspaceID, CreatedBy: ids.UserID, Name: "Latest Content", ConnectorType: "agent_daemon",
		AgentConfig:         map[string]any{"agent_kind": "claude_code", "daemon_mode": "local"},
		InitialCapabilities: []InitialAgentCapabilityInput{{CapabilityVersionID: versions[0].ID, PinningMode: PinningModeLatest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.GetEnabledCapabilitiesForAgent(ctx, created.Agent.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("enabled capabilities = %+v: %v", rows, err)
	}
	row := rows[0]
	if row.CapabilityVersionID != versions[0].ID || row.LatestVersionID != v2.ID || row.PinningMode != PinningModeLatest {
		t.Fatalf("unexpected version metadata: %+v", row)
	}
	for _, field := range []struct {
		name        string
		raw         []byte
		credentials []RequiredCredential
		command     string
		kind        string
	}{{"stored", row.Content, row.RequiredCredentials, "v1", "github_pat"}, {"latest", row.LatestContent, row.LatestRequiredCredentials, "v2", "slack_bot_token"}} {
		var parsed struct {
			MCPServers map[string]struct{ Command string }
		}
		if err := json.Unmarshal(field.raw, &parsed); err != nil || parsed.MCPServers["versioned"].Command != field.command {
			t.Fatalf("%s content mismatch: %s (%v)", field.name, field.raw, err)
		}
		if len(field.credentials) != 1 || field.credentials[0].Kind != field.kind || !field.credentials[0].Required {
			t.Fatalf("%s credentials mismatch: %+v", field.name, field.credentials)
		}
	}
}
