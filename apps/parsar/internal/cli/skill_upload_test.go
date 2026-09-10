package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginAddUsesUploadCredentialWithoutWorkspaceOrRunner(t *testing.T) {
	dir := t.TempDir()
	for file, content := range map[string]string{"manifest.json": `{"name":"guide","version":"1.0.0","skills":["guide.md"]}`, "guide.md": "Return GUIDE-OK."} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/agent-authoring/skill-bundles" || r.Header.Get("Authorization") != "Bearer upload-test" {
			t.Errorf("wrong upload endpoint or credential")
		}
		var payload struct {
			CanonicalSpec struct {
				Bundle struct{ Skills []bundleSkillPayload } `json:"bundle"`
			} `json:"canonical_spec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.CanonicalSpec.Bundle.Skills) != 1 || payload.CanonicalSpec.Bundle.Skills[0].Instruction != "Return GUIDE-OK." {
			t.Error("Skill file was not embedded")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"created"}`))
	}))
	defer server.Close()
	t.Setenv("PARSAR_SERVER_URL", server.URL)
	t.Setenv("PARSAR_CAPABILITY_UPLOAD_TOKEN", "upload-test")
	t.Setenv("PARSAR_RUNNER_TOKEN", "")
	t.Setenv("PARSAR_WORKSPACE_ID", "")
	ctx, stdout, _ := newCapturedCtx(nil)
	if err := execute(ctx, []string{"plugin", "add", "--json", dir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "created") || calls != 1 {
		t.Fatal("upload did not complete")
	}
	if err := execute(ctx, []string{"spec", "list"}); err == nil || !strings.Contains(err.Error(), "PARSAR_RUNNER_TOKEN") {
		t.Fatalf("upload credential escaped to another API: %v", err)
	}
	if calls != 1 {
		t.Fatal("sent an upload credential to another endpoint")
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"guide","version":"1.0.0","server":{"entry":"missing.js"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := execute(ctx, []string{"plugin", "add", dir}); err == nil || !strings.Contains(err.Error(), "inline Skills only") {
		t.Fatalf("code plugin was not rejected before local installation: %v", err)
	}
	if calls != 1 {
		t.Fatal("unsupported plugin was sent")
	}
}

func TestPluginAddRetainsLegacyEndpoint(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"guide","version":"1.0.0","skills":["guide.md"]}`), 0600)
	_ = os.WriteFile(filepath.Join(dir, "guide.md"), []byte("GUIDE-OK"), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces/workspace/capabilities/plugins/install" || r.Header.Get("Authorization") != "Bearer legacy" {
			t.Error("legacy upload changed")
		}
		_, _ = w.Write([]byte(`{"id":"created"}`))
	}))
	defer server.Close()
	ctx, _, _ := newCapturedCtx(&Config{ServerURL: server.URL, RunnerToken: "legacy", WorkspaceID: "workspace"})
	if err := execute(ctx, []string{"plugin", "add", dir}); err != nil {
		t.Fatal(err)
	}
}
