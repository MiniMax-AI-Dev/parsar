package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const skillPreviewTestBody = `{"source":"googleworkspace/cli","slug":"gws-gmail-triage","registry_id":"googleworkspace/cli/gws-gmail-triage","registry":"skills.sh"}`

func TestSkillPreviewFromRegistry_ReadOnlyAndCleansUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &fakeSkillInstallRunner{t: t, writeSkill: true}
	r, db, _ := skillsInstallTestRouter(t, runner)
	ids := store.DefaultDevFixtureIDs()
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/skills/preview", skillPreviewTestBody, ids.UserID)
	if res.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", res.Code, res.Body.String())
	}
	var result previewCapabilityImportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	skill := result.CanonicalSpec.Skill
	if skill == nil || result.SuggestedName != "Gmail Triage" || !strings.Contains(skill.Instruction, "Gmail labels") || len(skill.Files) != 2 {
		t.Fatalf("missing parsed instructions or files: %+v", result)
	}
	if skill.Description != "Triage Gmail with Workspace CLI" {
		t.Fatalf("description = %q", skill.Description)
	}
	var count int
	if err := db.QueryRow(context.Background(), "select count(*) from capability where workspace_id = $1 and type = 'skill'", ids.WorkspaceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("preview created %d capabilities", count)
	}
	installs, err := store.New(db).ListSkillsDirectoryInstalls(context.Background(), ids.WorkspaceID)
	if err != nil || len(installs) != 0 {
		t.Fatalf("preview persisted installation: %v, %v", installs, err)
	}
	if !strings.HasPrefix(runner.lastDir, filepath.Join(os.Getenv("HOME"), ".parsar")+string(os.PathSeparator)) {
		t.Fatalf("preview storage escaped Parsar root: %s", runner.lastDir)
	}
	if _, err := os.Stat(runner.lastDir); !os.IsNotExist(err) {
		t.Fatalf("preview directory not removed: %v", err)
	}
}

func TestSkillPreviewFromRegistry_DeniesBeforeDownload(t *testing.T) {
	for _, tc := range []struct {
		name, role, user, body string
		status                 int
	}{
		{"anonymous", "admin", "", skillPreviewTestBody, http.StatusUnauthorized},
		{"member", "member", "user", skillPreviewTestBody, http.StatusForbidden},
		{"invalid source", "admin", "user", strings.Replace(skillPreviewTestBody, "googleworkspace/cli", "../cli", 1), http.StatusBadRequest},
		{"option slug", "admin", "user", strings.Replace(skillPreviewTestBody, "gws-gmail-triage", "--global", 1), http.StatusBadRequest},
		{"option owner", "admin", "user", strings.Replace(skillPreviewTestBody, "googleworkspace/cli", "-owner/cli", 1), http.StatusBadRequest},
		{"local source", "admin", "user", strings.Replace(skillPreviewTestBody, "googleworkspace/cli", "/googleworkspace/cli", 1), http.StatusBadRequest},
		{"invalid json", "admin", "user", "{", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeSkillInstallRunner{t: t}
			rbac := capabilityRBACStore{workspaceRoles: map[string]string{"user": tc.role}}
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, rbac, WithSkillInstallRunner(runner), WithAuthMiddleware(auth.NewMiddleware(newStubSessions()).WithDevAuth(true)))
			req := newRequestWithDevUser(http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/skills/preview", tc.body, tc.user)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != tc.status || runner.called {
				t.Fatalf("status = %d, runner called = %v, body = %s", res.Code, runner.called, res.Body.String())
			}
		})
	}
}

func TestSkillPreviewFromRegistry_FailedDownloadCleansUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &fakeSkillInstallRunner{t: t, returnError: errors.New("repository unavailable")}
	rbac := capabilityRBACStore{workspaceRoles: map[string]string{"user": "admin"}}
	r := chi.NewRouter()
	r.Post("/workspaces/{workspaceID}/skills/preview", previewSkillFromRegistry(rbac, runner))
	res := serveCapabilityRoute(t, r, http.MethodPost, "/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/skills/preview", skillPreviewTestBody, "user")
	if res.Code != http.StatusBadGateway || !runner.called {
		t.Fatalf("status = %d, runner called = %v", res.Code, runner.called)
	}
	if _, err := os.Stat(runner.lastDir); !os.IsNotExist(err) {
		t.Fatalf("failed preview directory not removed: %v", err)
	}
}
