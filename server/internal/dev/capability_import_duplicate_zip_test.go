package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestCapabilityImportRejectsDuplicateZipAtEveryEntryPoint(t *testing.T) {
	r, db, blobs := skillsInstallTestRouter(t, &fakeSkillInstallRunner{t: t})
	ids := store.DefaultDevFixtureIDs()
	base := "/api/v1/workspaces/" + ids.WorkspaceID + "/capabilities"
	storeZip := func(entries []struct{ name, content string }) string {
		t.Helper()
		ref, err := blobs.NewRef("skill", ids.WorkspaceID, "skill.zip")
		if err != nil {
			t.Fatal(err)
		}
		if err := blobs.PutBytes(t.Context(), ref, ids.WorkspaceID, makeSkillZip(t, entries)); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	validRef := storeZip([]struct{ name, content string }{{"SKILL.md", goodSkillMd}})
	res := serveCapabilityRoute(t, r, http.MethodPost, base+"/import/commit", mustJSON(t, map[string]any{
		"kind": "skill", "name": "ZIP path test", "oss_key": validRef,
	}), ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("valid import: %d %s", res.Code, res.Body.String())
	}
	var created commitCapabilityImportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	duplicateRef := storeZip([]struct{ name, content string }{
		{"SKILL.md", goodSkillMd + "PREVIEW-188"},
		{"SKILL.md", goodSkillMd + "RUNTIME-299"},
	})
	for _, endpoint := range []string{"/import/preview", "/import/commit", "/" + created.Capability.ID + "/versions/import/commit"} {
		res := serveCapabilityRoute(t, r, http.MethodPost, base+endpoint, mustJSON(t, map[string]any{
			"kind": "skill", "name": "Duplicate ZIP", "source_format": "zip", "oss_key": duplicateRef,
			"canonical_spec": created.CapabilityVersion.CanonicalSpec,
		}), ids.UserID)
		if res.Code < 400 || res.Code >= 500 || !strings.Contains(res.Body.String(), "duplicate zip path") {
			t.Fatalf("%s accepted duplicate ZIP: %d %s", endpoint, res.Code, res.Body.String())
		}
	}
	var count int
	if err := db.QueryRow(t.Context(), "SELECT count(*) FROM capability_version").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rejected imports created versions: count=%d", count)
	}
}
