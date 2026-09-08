package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func (stubRuntimeStore) ListSkillsDirectoryInstalls(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestSkillsDirectoryInstalledStatePersists(t *testing.T) {
	runner := &fakeSkillInstallRunner{t: t, writeSkill: true}
	installRouter, db, _ := skillsInstallTestRouter(t, runner)
	ids := store.DefaultDevFixtureIDs()
	prefix := "/api/v1/workspaces/" + ids.WorkspaceID + "/skills/"
	registryID := "googleworkspace/cli/gws-gmail-triage"
	res := serveCapabilityRoute(t, installRouter, http.MethodPost, prefix+"install", mustJSON(t, map[string]string{
		"source": "googleworkspace/cli", "slug": "gws-gmail-triage", "registry_id": registryID, "registry": "skills.sh",
	}), ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", res.Code, res.Body.String())
	}
	var installed commitCapabilityImportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, st)
	read := func(wantID string) {
		t.Helper()
		res := serveCapabilityRoute(t, r, http.MethodGet, prefix+"installed", "", ids.UserID)
		var got map[string]string
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &got) != nil {
			t.Fatalf("read installs: %d %s", res.Code, res.Body.String())
		}
		if wantID == "" && len(got) != 0 || wantID != "" && (len(got) != 1 || got[registryID] != wantID) {
			t.Fatalf("installs = %v, want capability %q", got, wantID)
		}
	}
	read(installed.Capability.ID)
	ctx := context.Background()
	renamed := "Renamed installed skill"
	if _, err := st.UpdateCapability(ctx, store.UpdateCapabilityInput{CapabilityID: installed.Capability.ID, Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCapabilityVersion(ctx, store.CreateCapabilityVersionInput{
		CapabilityID: installed.Capability.ID, Version: "1.0.1", CreatorID: ids.UserID,
		CanonicalSpec: installed.CapabilityVersion.CanonicalSpec,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeprecateCapability(ctx, ids.WorkspaceID, installed.Capability.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCapability(ctx, store.CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID, Name: installed.Capability.Name, Type: "skill", CreatorID: ids.UserID,
	}); err != nil {
		t.Fatal(err)
	}
	read(installed.Capability.ID)
	for _, test := range []struct{ workspaceID, userID string }{
		{ids.WorkspaceID, "22222222-2222-4222-8222-222222222222"},
		{"11111111-1111-4111-8111-111111111111", ids.UserID},
	} {
		res := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+test.workspaceID+"/skills/installed", "", test.userID)
		if res.Code != http.StatusNotFound {
			t.Fatalf("unauthorized read: %d %s", res.Code, res.Body.String())
		}
	}
	if _, err := st.SoftDeleteCapability(ctx, ids.WorkspaceID, installed.Capability.ID); err != nil {
		t.Fatal(err)
	}
	read("")
}
