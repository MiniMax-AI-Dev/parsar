package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestGenericSkillVersionWritesRequireImport(t *testing.T) {
	ids := store.DefaultDevFixtureIDs()
	router, db := capabilityTestRouter(t, map[string]string{ids.UserID: "admin", testUserAID: "member"}, nil)
	st := store.New(db)
	base := "/api/v1/workspaces/" + ids.WorkspaceID + "/capabilities"
	spec := canonical.Spec{SchemaVersion: 1, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{
		Slug: "archive-required", Instruction: "Return SKILL-OK.",
	}}

	for _, canonicalInput := range []bool{false, true} {
		body := map[string]any{"type": " Skill ", "name": "Unusable Skill", "version": "1.0.0"}
		if canonicalInput {
			body["canonical_spec"] = spec
		} else {
			body["content"] = map[string]any{"instruction": "Return SKILL-OK."}
		}
		res := serveCapabilityRoute(t, router, http.MethodPost, base, mustJSON(t, body), ids.UserID)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "/capabilities/import/commit") {
			t.Fatalf("initial version must direct callers to import: %d %s", res.Code, res.Body.String())
		}
	}
	var count int
	if err := db.QueryRow(t.Context(), `select count(*) from capability where name = 'Unusable Skill'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected creation wrote metadata: count=%d, error=%v", count, err)
	}

	created := serveCapabilityRoute(t, router, http.MethodPost, base, `{"type":"skill","name":"Skill metadata"}`, ids.UserID)
	if created.Code != http.StatusCreated {
		t.Fatalf("metadata creation: %d %s", created.Code, created.Body.String())
	}
	var capability store.CapabilityRead
	if err := json.Unmarshal(created.Body.Bytes(), &capability); err != nil {
		t.Fatal(err)
	}
	endpoint := base + "/" + capability.ID + "/versions"
	for _, canonicalInput := range []bool{false, true} {
		body := map[string]any{"version": "1.0.0"}
		if canonicalInput {
			body["canonical_spec"] = spec
		}
		res := serveCapabilityRoute(t, router, http.MethodPost, endpoint, mustJSON(t, body), ids.UserID)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "/versions/import/commit") {
			t.Fatalf("new version must direct callers to import: %d %s", res.Code, res.Body.String())
		}
	}
	versions, err := st.ListCapabilityVersions(t.Context(), capability.ID)
	if err != nil || len(versions) != 0 {
		t.Fatalf("rejected requests wrote versions: count=%d, error=%v", len(versions), err)
	}
	denied := serveCapabilityRoute(t, router, http.MethodPost, endpoint, `{"version":"1.0.0"}`, testUserAID)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("member authorization changed: %d %s", denied.Code, denied.Body.String())
	}
}
