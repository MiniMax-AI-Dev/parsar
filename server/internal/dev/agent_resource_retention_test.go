package dev

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"net/http"
	"testing"
)

func TestAgentEditsRetainMarketplaceResourcesWithoutPrivateUpgrades(t *testing.T) {
	for _, kind := range []string{"skill", "knowledge"} {
		for _, mode := range []string{"pinned", "latest"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ids := store.DefaultDevFixtureIDs()
				router, db := capabilityTestRouter(t, map[string]string{ids.UserID: "admin"}, nil)
				st, ctx := store.New(db), t.Context()
				source, err := st.CreateWorkspace(ctx, store.CreateWorkspaceInput{Name: "Resource source", CreatedBy: ids.UserID})
				if err != nil {
					t.Fatal(err)
				}
				cap, err := st.CreateCapability(ctx, store.CreateCapabilityInput{WorkspaceID: source.Workspace.ID, Type: kind, Name: "Retained resource", Visibility: "public", CreatorID: ids.UserID, InitialVersion: &store.CreateCapabilityVersionInput{Version: "1", CreatorID: ids.UserID}})
				if err != nil {
					t.Fatal(err)
				}
				versions, err := st.ListCapabilityVersions(ctx, cap.ID)
				if err != nil {
					t.Fatal(err)
				}
				original := versions[0].ID
				agent, err := st.CreateAgent(ctx, store.CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "Consumer", ConnectorType: "agents_api", CreatedBy: ids.UserID, AgentConfig: map[string]any{"model": "fixture"}, InitialCapabilities: []store.InitialAgentCapabilityInput{{CapabilityVersionID: original, PinningMode: mode}}})
				if err != nil {
					t.Fatal(err)
				}
				binding := store.InitialAgentCapabilityInput{CapabilityVersionID: original, PinningMode: mode}
				for _, state := range []string{"deprecated", "unpublished"} {
					if state == "deprecated" {
						_, err = st.DeprecateCapability(ctx, source.Workspace.ID, cap.ID)
					} else {
						if _, err := st.UndeprecateCapability(ctx, source.Workspace.ID, cap.ID); err != nil {
							t.Fatal(err)
						}
						_, err = st.UnpublishCapability(ctx, source.Workspace.ID, cap.ID)
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, include := range []bool{false, true} {
						body := map[string]any{"name": "Renamed consumer"}
						if include {
							body["resource_bindings"] = []store.InitialAgentCapabilityInput{binding}
						}
						res := serveCapabilityRoute(t, router, http.MethodPatch, "/api/v1/agents/"+agent.Agent.ID, mustJSON(t, body), ids.UserID)
						if res.Code != http.StatusOK {
							t.Fatalf("%s include=%v: %d %s", state, include, res.Code, res.Body.String())
						}
					}
				}
				private, err := st.CreateCapabilityVersion(ctx, store.CreateCapabilityVersionInput{CapabilityID: cap.ID, Version: "2", CreatorID: ids.UserID})
				if err != nil {
					t.Fatal(err)
				}
				rows, err := st.GetEnabledCapabilitiesForAgent(ctx, agent.Agent.ID)
				if err != nil || len(rows) != 1 || rows[0].LatestVersionID != original {
					t.Fatalf("private version exposed: %+v %v", rows, err)
				}
				// The retained pin remains editable after a new private revision exists.
				res := serveCapabilityRoute(t, router, http.MethodPatch, "/api/v1/agents/"+agent.Agent.ID, mustJSON(t, map[string]any{"name": "Still retained", "resource_bindings": []store.InitialAgentCapabilityInput{binding}}), ids.UserID)
				if res.Code != http.StatusOK {
					t.Fatalf("retained revision: %d %s", res.Code, res.Body.String())
				}
				binding.CapabilityVersionID = private.ID
				res = serveCapabilityRoute(t, router, http.MethodPatch, "/api/v1/agents/"+agent.Agent.ID, mustJSON(t, map[string]any{"resource_bindings": []store.InitialAgentCapabilityInput{binding}}), ids.UserID)
				if res.Code < 400 {
					t.Fatal("Agent edit acquired private revision")
				}
			})
		}
	}
}
