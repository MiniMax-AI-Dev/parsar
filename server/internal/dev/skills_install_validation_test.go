package dev

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestSkillInstallRejectsCLIOptionsAndLocalPaths(t *testing.T) {
	for _, tc := range []struct{ source, slug string }{
		{"owner/repo", "--global"},
		{"owner/repo", "-g"},
		{"owner/repo", "--"},
		{"-owner/repo", "skill"},
		{"/owner/repo", "skill"},
		{"owner/repo/", "skill"},
	} {
		t.Run(tc.source+":"+tc.slug, func(t *testing.T) {
			runner := &fakeSkillInstallRunner{t: t}
			ids := store.DefaultDevFixtureIDs()
			rbac := capabilityRBACStore{workspaceRoles: map[string]string{ids.UserID: "admin"}}
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, rbac, WithBlobStore(blob.NewMemoryStore("https://api.test")), WithSkillInstallRunner(runner))
			body := mustJSON(t, installSkillRequest{Source: tc.source, Slug: tc.slug, RegistryID: "test", Registry: skillsRegistryName})
			res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/skills/install", body, ids.UserID)
			if res.Code != http.StatusBadRequest || runner.called {
				t.Fatalf("status = %d, runner called = %v, response = %s", res.Code, runner.called, res.Body.String())
			}
		})
	}
}

func TestSkillInstallAcceptsSafeRepositoryReferences(t *testing.T) {
	for _, source := range []string{"owner/repo", "my-org/skill.repo", "owner/-repo", " owner/repo "} {
		for _, slug := range []string{"skill", "my-skill", "skill_v1.0"} {
			if msg := validateInstallSkillRequest(installSkillRequest{Source: source, Slug: slug, RegistryID: "test", Registry: skillsRegistryName}); msg != "" {
				t.Errorf("rejected source %q, slug %q: %s", source, slug, msg)
			}
		}
	}
}
