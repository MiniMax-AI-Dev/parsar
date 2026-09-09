package dev

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestLatestMCPCredentialBinding(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{testUserAID: "member"}, nil)
	workspaceID := store.DefaultDevFixtureIDs().WorkspaceID
	capID, v1, _ := insertCapabilityVersions(t, db, workspaceID, "Latest credentials")
	if _, err := db.Exec(t.Context(), `update capability_version set required_credentials = '[]' where id = $1`, v1); err != nil {
		t.Fatal(err)
	}
	agentID := insertAgentForOwner(t, db, testUserAID, "latest-credentials")
	if _, err := db.Exec(t.Context(), `update agents set visibility = 'public', config = '{"agent_kind":"claude_code"}' where id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + workspaceID + "/agents/" + agentID + "/capabilities"
	for _, mode := range []string{"pinned", "", "latest"} {
		response := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable", fmt.Sprintf(`{"pinning_mode":%q}`, mode), testUserAID)
		want := http.StatusOK
		if mode == "latest" {
			want = http.StatusUnprocessableEntity
		}
		if response.Code != want {
			t.Fatalf("public %q: %d %s", mode, response.Code, response.Body.String())
		}
	}
	secretID := "00000000-0000-0000-0000-000000000099"
	if _, err := db.Exec(t.Context(), `insert into secrets(id, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_by, created_at, updated_at)
		values ($1, 'latest-shared', 'Latest shared credential', 'capability_inline', 'inline', 'literal', '{}'::jsonb, 'v1', 'active', $2::jsonb, $3, now(), now())`,
		secretID, `{"credential_kind_code":"github_pat"}`, testUserAID); err != nil {
		t.Fatal(err)
	}
	shared := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable",
		`{"pinning_mode":"latest","configuration":{"credential_bindings":{"github_pat":{"source":"shared","secret_id":"`+secretID+`"}}}}`, testUserAID)
	if shared.Code != http.StatusOK {
		t.Fatalf("shared binding for the selected version: %d %s", shared.Code, shared.Body.String())
	}
	if _, err := db.Exec(t.Context(), `update agents set visibility = 'workspace' where id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	response := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable", `{"pinning_mode":"latest"}`, testUserAID)
	if response.Code != http.StatusOK {
		t.Fatalf("workspace latest: %d %s", response.Code, response.Body.String())
	}
	response = serveCapabilityRoute(t, r, http.MethodGet, base, "", testUserAID)
	var body struct {
		Installed []struct {
			store.AgentCapabilityRead
			Capability struct {
				RequiredCredentials []store.RequiredCredential `json:"required_credentials"`
			} `json:"capability"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("list: %d %s", response.Code, response.Body.String())
	}
	for _, binding := range body.Installed {
		if binding.CapabilityID != capID {
			continue
		}
		if binding.CapabilityVersionID != v1 || binding.PinningMode != "latest" || len(binding.Capability.RequiredCredentials) != 1 || binding.Capability.RequiredCredentials[0].Kind != "github_pat" {
			t.Fatalf("selected credential declaration or stored identity changed: %+v", binding)
		}
		return
	}
	t.Fatal("installed MCP missing")
}
