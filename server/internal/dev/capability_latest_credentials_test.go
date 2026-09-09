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

func TestLatestMCPRetiredCredentialVisibility(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{testUserAID: "admin"}, nil)
	workspaceID := store.DefaultDevFixtureIDs().WorkspaceID
	capID, v1, v2 := insertCapabilityVersions(t, db, workspaceID, "Retired credential")
	agentID := insertAgentForOwner(t, db, testUserAID, "retired-credential")
	secretID := "00000000-0000-0000-0000-000000000099"
	if _, err := db.Exec(t.Context(), `insert into secrets(id, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_by, created_at, updated_at)
		values ($1, 'retired-shared', 'Retired shared credential', 'capability_inline', 'inline', 'literal', '{}'::jsonb, 'v1', 'active', '{"credential_kind_code":"github_pat"}', $2, now(), now())`, secretID, testUserAID); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + workspaceID + "/agents/" + agentID + "/capabilities"
	body := `{"pinning_mode":"latest","configuration":{"credential_bindings":{"github_pat":{"source":"shared","secret_id":"` + secretID + `"}}}}`
	enabled := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable", body, testUserAID)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable before retiring credential: %d %s", enabled.Code, enabled.Body.String())
	}
	if _, err := db.Exec(t.Context(), `update capability_version set required_credentials = '[]' where id = $1`, v2); err != nil {
		t.Fatal(err)
	}
	visibilityPath := "/api/v1/agents/" + agentID + "/visibility"
	published := serveCapabilityRoute(t, r, http.MethodPatch, visibilityPath, `{"visibility":"public"}`, testUserAID)
	if published.Code != http.StatusOK {
		t.Fatalf("publish with retired stored credential: %d %s", published.Code, published.Body.String())
	}
	var retainedSecret string
	if err := db.QueryRow(t.Context(), `select configuration->'credential_bindings'->'github_pat'->>'secret_id' from agent_capabilities where agent_id = $1 and capability_id = $2`, agentID, capID).Scan(&retainedSecret); err != nil || retainedSecret != secretID {
		t.Fatalf("stored binding changed: %q, %v", retainedSecret, err)
	}
	resubmitted := serveCapabilityRoute(t, r, http.MethodPost, base+"/"+v1+"/enable", body, testUserAID)
	if resubmitted.Code != http.StatusUnprocessableEntity {
		t.Fatalf("new configuration containing an unused credential: %d %s", resubmitted.Code, resubmitted.Body.String())
	}
	if _, err := db.Exec(t.Context(), `update agents set visibility = 'workspace' where id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `update capability_version set required_credentials = '[{"kind":"notion_token","required":true}]' where id = $1`, v2); err != nil {
		t.Fatal(err)
	}
	published = serveCapabilityRoute(t, r, http.MethodPatch, visibilityPath, `{"visibility":"public"}`, testUserAID)
	if published.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish without the new required shared credential: %d %s", published.Code, published.Body.String())
	}
}
