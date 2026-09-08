package dev

import (
	"context"
	"net/http"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestCapabilityUpgradeVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, state string
		foreign                 bool
		want                    int
	}{
		{"own workspace private", "workspace", "active", false, http.StatusOK},
		{"foreign public", "public", "active", true, http.StatusOK},
		{"foreign private", "workspace", "active", true, http.StatusForbidden},
		{"foreign deprecated", "public", "deprecated", true, http.StatusForbidden},
		{"own deprecated", "workspace", "deprecated", false, http.StatusForbidden},
		{"own deleted", "workspace", "deleted", false, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := capabilityTestRouter(t, map[string]string{testUserAID: "member"}, nil)
			workspaceID := store.DefaultDevFixtureIDs().WorkspaceID
			sourceID := workspaceID
			if tc.foreign {
				sourceID = "00000000-0000-0000-0000-000000000099"
				insertForeignWorkspace(t, db, sourceID)
			}
			capID, v1, v2 := insertCapabilityVersions(t, db, sourceID, "Upgrade visibility")
			publishForeignCapability(t, db, capID)
			agentID := insertAgentForOwner(t, db, testUserAID, "upgrade-visibility-agent")
			base := "/api/v1/workspaces/" + workspaceID + "/agents/" + agentID + "/capabilities/"
			enabled := serveCapabilityRoute(t, r, http.MethodPost, base+v1+"/enable", `{}`, testUserAID)
			if enabled.Code != http.StatusOK {
				t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
			}
			if _, err := db.Exec(context.Background(), `update capability set visibility = $2,
				deprecated_at = case when $3 = 'deprecated' then now() else null end,
				deleted_at = case when $3 = 'deleted' then now() else null end where id = $1`, capID, tc.visibility, tc.state); err != nil {
				t.Fatal(err)
			}
			upgraded := serveCapabilityRoute(t, r, http.MethodPost, base+capID+"/upgrade",
				`{"new_version_id":"`+v2+`","pinning_mode":"pinned"}`, testUserAID)
			if upgraded.Code != tc.want {
				t.Fatalf("upgrade: %d %s, want %d", upgraded.Code, upgraded.Body.String(), tc.want)
			}
			wantVersion := v1
			if tc.want == http.StatusOK {
				wantVersion = v2
			}
			assertSingleAgentCapability(t, db, agentID, capID, wantVersion)
		})
	}
}
