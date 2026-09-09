package dev

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestAgentCapabilitiesExposeVersionMode(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{testUserAID: "member"}, nil)
	workspaceID := store.DefaultDevFixtureIDs().WorkspaceID
	capabilityID, v1, v2 := insertCapabilityVersions(t, db, workspaceID, "Version mode")
	agentID := insertAgentForOwner(t, db, testUserAID, "version-mode-agent")
	base := "/api/v1/workspaces/" + workspaceID + "/agents/" + agentID + "/capabilities"
	for _, mode := range []string{"latest", "pinned"} {
		enabled := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable",
			`{"pinning_mode":"`+mode+`"}`, testUserAID)
		if enabled.Code != http.StatusOK {
			t.Fatalf("enable %s: %d %s", mode, enabled.Code, enabled.Body.String())
		}
		response := serveCapabilityRoute(t, r, http.MethodGet, base, "", testUserAID)
		var body struct {
			Installed []struct {
				store.AgentCapabilityRead
				Capability struct {
					LatestVersionID string `json:"latest_version_id"`
					PinnedVersionID string `json:"pinned_version_id"`
				} `json:"capability"`
			} `json:"installed"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
			t.Fatalf("list: %d %s", response.Code, response.Body.String())
		}
		found := false
		for _, binding := range body.Installed {
			if binding.CapabilityID != capabilityID {
				continue
			}
			found = true
			if binding.PinningMode != mode || binding.CapabilityVersionID != v1 ||
				binding.Capability.LatestVersionID != v2 || binding.Capability.PinnedVersionID != v1 {
				t.Fatalf("mode %s: unexpected version metadata: %+v", mode, binding)
			}
		}
		if !found {
			t.Fatal("installed capability missing")
		}
	}
}
