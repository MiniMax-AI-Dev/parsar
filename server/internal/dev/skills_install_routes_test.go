package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeSkillInstallRunner struct {
	t              *testing.T
	called         bool
	lastDir        string
	lastName       string
	lastArgs       []string
	writeSkill     bool
	returnOutput   []byte
	returnError    error
	writtenRefPath string
}

func (r *fakeSkillInstallRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	r.t.Helper()
	r.called = true
	r.lastDir = dir
	r.lastName = name
	r.lastArgs = append([]string(nil), args...)
	if r.writeSkill {
		skillDir := filepath.Join(dir, ".claude", "skills", "gws-gmail-triage")
		if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
			r.t.Fatalf("create fake skill references dir: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
			r.t.Fatalf("create fake skill scripts dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Gmail Triage\ndescription: Triage Gmail with Workspace CLI\n---\nUse Gmail labels and search operators to triage mail.\n"), 0o644); err != nil {
			r.t.Fatalf("write fake SKILL.md: %v", err)
		}
		r.writtenRefPath = filepath.Join(skillDir, "references", "triage.md")
		if err := os.WriteFile(r.writtenRefPath, []byte("# Triage\nCheck labels first.\n"), 0o644); err != nil {
			r.t.Fatalf("write fake reference: %v", err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "scripts", "triage.sh"), []byte("#!/bin/sh\necho triage\n"), 0o755); err != nil {
			r.t.Fatalf("write fake script: %v", err)
		}
	}
	return r.returnOutput, r.returnError
}

func skillsInstallTestRouter(t *testing.T, runner skillInstallCommandRunner) (http.Handler, *pgxpool.Pool, *blob.MemoryStore) {
	t.Helper()
	db := openDevRouteTestDB(t)
	s := store.New(db)
	if _, err := s.SeedDevFixture(context.Background()); err != nil {
		t.Fatal(err)
	}
	rbac := &capabilityRBACStore{RuntimeStore: s, workspaceRoles: map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}}
	bs := blob.NewMemoryStore("https://api.test")
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, rbac, WithBlobStore(bs), WithSkillInstallRunner(runner))
	return r, db, bs
}

func TestSkillInstallFromRegistry_HappyPathReusesSkillZipImport(t *testing.T) {
	runner := &fakeSkillInstallRunner{t: t, writeSkill: true, returnOutput: []byte("added gws-gmail-triage")}
	r, db, bs := skillsInstallTestRouter(t, runner)
	ids := store.DefaultDevFixtureIDs()

	body := mustJSON(t, map[string]any{
		"source":      "googleworkspace/cli",
		"slug":        "gws-gmail-triage",
		"registry_id": "googleworkspace/cli/gws-gmail-triage",
		"registry":    "skills.sh",
	})
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/skills/install", body, ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("install expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if !runner.called {
		t.Fatal("skills runner was not called")
	}
	wantArgs := []string{"--yes", "skills@1.5.23", "add", "googleworkspace/cli", "--skill", "gws-gmail-triage", "--agent", "claude-code", "--copy", "--yes"}
	if runner.lastName != "npx" || !reflect.DeepEqual(runner.lastArgs, wantArgs) {
		t.Fatalf("runner command = %s %v, want npx %v", runner.lastName, runner.lastArgs, wantArgs)
	}

	var parsed commitCapabilityImportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode install response: %v\nbody=%s", err, res.Body.String())
	}
	if parsed.Capability.Type != string(canonical.KindSkill) {
		t.Fatalf("capability type = %q, want skill", parsed.Capability.Type)
	}
	if parsed.Capability.Name != "Gmail Triage" {
		t.Fatalf("capability name = %q, want Gmail Triage", parsed.Capability.Name)
	}
	if parsed.CapabilityVersion.OssKey == "" || !strings.HasPrefix(parsed.CapabilityVersion.OssKey, "pg:") {
		t.Fatalf("oss_key = %q, want pg ref", parsed.CapabilityVersion.OssKey)
	}
	if len(parsed.CapabilityVersion.SHA256) != 64 {
		t.Fatalf("sha256 = %q, want 64-char hex", parsed.CapabilityVersion.SHA256)
	}
	if _, err := bs.Download(context.Background(), parsed.CapabilityVersion.OssKey); err != nil {
		t.Fatalf("stored zip not downloadable from memory blob: %v", err)
	}

	var spec canonical.Spec
	if err := json.Unmarshal(lookupCanonicalSpec(t, db, parsed.CapabilityVersion.ID), &spec); err != nil {
		t.Fatalf("decode canonical_spec: %v", err)
	}
	if spec.Kind != canonical.KindSkill || spec.Skill == nil {
		t.Fatalf("canonical spec should be a skill, got %+v", spec)
	}
	if spec.Skill.Slug != "gmail-triage" {
		t.Fatalf("skill slug = %q, want gmail-triage", spec.Skill.Slug)
	}
	filesByPath := map[string]canonical.SkillFile{}
	for _, file := range spec.Skill.Files {
		filesByPath[file.Path] = file
	}
	if filesByPath["references/triage.md"].Kind != canonical.SkillFileKindMarkdown {
		t.Fatalf("references/triage.md missing or wrong kind: %+v", filesByPath["references/triage.md"])
	}
	if filesByPath["scripts/triage.sh"].Kind != canonical.SkillFileKindScript {
		t.Fatalf("scripts/triage.sh missing or wrong kind: %+v", filesByPath["scripts/triage.sh"])
	}

	var sourcePayload []byte
	if err := db.QueryRow(context.Background(), "select source_payload from capability_version where id = $1", parsed.CapabilityVersion.ID).Scan(&sourcePayload); err != nil {
		t.Fatalf("lookup source_payload: %v", err)
	}
	var source map[string]string
	if err := json.Unmarshal(sourcePayload, &source); err != nil {
		t.Fatalf("decode source_payload: %v", err)
	}
	for key, want := range map[string]string{
		"registry":    "skills.sh",
		"registry_id": "googleworkspace/cli/gws-gmail-triage",
		"source":      "googleworkspace/cli",
		"slug":        "gws-gmail-triage",
	} {
		if source[key] != want {
			t.Fatalf("source_payload[%s] = %q, want %q (payload=%s)", key, source[key], want, string(sourcePayload))
		}
	}
	if _, err := os.Stat(runner.lastDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir should be removed after install, stat err=%v", err)
	}
}

func TestSkillInstallCommandEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	env, err := skillInstallCommandEnv()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["DISABLE_TELEMETRY"] != "1" {
		t.Fatalf("DISABLE_TELEMETRY = %q, want 1", values["DISABLE_TELEMETRY"])
	}
	wantCache := filepath.Join(home, ".parsar", "cache", "npm")
	if values["NPM_CONFIG_CACHE"] != wantCache {
		t.Fatalf("NPM_CONFIG_CACHE = %q, want %q", values["NPM_CONFIG_CACHE"], wantCache)
	}
	wantCompileCache := filepath.Join(home, ".parsar", "cache", "node")
	if values["NODE_COMPILE_CACHE"] != wantCompileCache {
		t.Fatalf("NODE_COMPILE_CACHE = %q, want %q", values["NODE_COMPILE_CACHE"], wantCompileCache)
	}
}

func TestSkillInstallFromRegistry_MissingSkillMarkdownFails(t *testing.T) {
	runner := &fakeSkillInstallRunner{t: t, writeSkill: false, returnOutput: []byte("added nothing")}
	r, db, _ := skillsInstallTestRouter(t, runner)
	ids := store.DefaultDevFixtureIDs()
	body := mustJSON(t, map[string]any{
		"source":      "googleworkspace/cli",
		"slug":        "gws-gmail-triage",
		"registry_id": "googleworkspace/cli/gws-gmail-triage",
		"registry":    "skills.sh",
	})
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/skills/install", body, ids.UserID)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("install expected 502 for missing SKILL.md, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "SKILL.md") {
		t.Fatalf("response should mention missing SKILL.md, got: %s", res.Body.String())
	}
	var count int
	if err := db.QueryRow(context.Background(), "select count(*) from capability where workspace_id = $1 and type = 'skill'", ids.WorkspaceID).Scan(&count); err != nil {
		t.Fatalf("count skill capabilities: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no skill capability to be created, got %d", count)
	}
	if _, err := os.Stat(runner.lastDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir should be removed after failed install, stat err=%v", err)
	}
}

func TestSkillInstallFromRegistry_InvalidRegistryRejected(t *testing.T) {
	runner := &fakeSkillInstallRunner{t: t, writeSkill: true}
	r, _, _ := skillsInstallTestRouter(t, runner)
	ids := store.DefaultDevFixtureIDs()
	body := mustJSON(t, map[string]any{
		"source":      "googleworkspace/cli",
		"slug":        "gws-gmail-triage",
		"registry_id": "googleworkspace/cli/gws-gmail-triage",
		"registry":    "other",
	})
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/skills/install", body, ids.UserID)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("install expected 400 for invalid registry, got %d: %s", res.Code, res.Body.String())
	}
	if runner.called {
		t.Fatal("runner should not be called for invalid registry")
	}
}
