package store

import (
	"context"
	"testing"
)

func TestUnpublishedMarketplaceInstallRemainsManageable(t *testing.T) {
	ctx := context.Background()
	st := New(openTestDB(t))
	ids := mustSeedDevFixture(t, ctx, st)
	source, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Unpublished source", CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: source.Workspace.ID, CreatorID: ids.UserID,
		Type: "mcp", Name: "Installed tool", Visibility: "public",
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
	if _, err := st.EnableAgentCapability(ctx, ids.ProductAgentID, versionID, nil, PinningModePinned); err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []string{"public", "workspace"} {
		if visibility == "workspace" {
			if _, err := st.UnpublishCapability(ctx, source.Workspace.ID, capability.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateCapabilityVersion(ctx, CreateCapabilityVersionInput{
				CapabilityID: capability.ID, Version: "2.0.0", CreatorID: ids.UserID, Content: map[string]any{},
			}); err != nil {
				t.Fatal(err)
			}
		}
		installs, err := st.ListWorkspaceMarketplaceInstalls(ctx, ids.WorkspaceID)
		if err != nil || len(installs) != 1 {
			t.Fatalf("%s installs = %+v, error = %v", visibility, installs, err)
		}
		item := installs[0]
		if item.CapabilityID != capability.ID || item.Visibility != visibility || item.PinnedVersionID != versionID || item.LatestVersionID != versionID || item.LatestPublishedVersion != "1.0.0" || item.EnabledAgentCount != 1 {
			t.Fatalf("unexpected %s install metadata: %+v", visibility, item)
		}
	}
	market, err := st.ListMarketplaceCapabilities(ctx, ids.WorkspaceID)
	if err != nil || len(market) != 0 {
		t.Fatalf("unpublished source exposed in marketplace: %+v, error = %v", market, err)
	}
	unrelated, err := st.ListWorkspaceMarketplaceInstalls(ctx, source.Workspace.ID)
	if err != nil || len(unrelated) != 0 {
		t.Fatalf("install exposed outside consuming workspace: %+v, error = %v", unrelated, err)
	}
	agents, err := st.ListEnabledAgents(ctx, ids.WorkspaceID, capability.ID)
	if err != nil || len(agents) != 1 || agents[0].AgentID != ids.ProductAgentID || agents[0].CapabilityVersionID != versionID {
		t.Fatalf("binding changed after unpublishing: %+v, error = %v", agents, err)
	}
	removed, err := st.UninstallWorkspaceMarketplaceCapability(ctx, ids.WorkspaceID, capability.ID)
	if err != nil || removed != 1 {
		t.Fatalf("uninstall = %d, error = %v", removed, err)
	}
	installs, err := st.ListWorkspaceMarketplaceInstalls(ctx, ids.WorkspaceID)
	if err != nil || len(installs) != 0 {
		t.Fatalf("install remains after removal: %+v, error = %v", installs, err)
	}
	if _, err := st.GetCapability(ctx, capability.ID); err != nil {
		t.Fatalf("uninstall changed source capability: %v", err)
	}
}
