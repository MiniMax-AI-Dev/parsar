package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
)

func TestBuildArgsUsesOpenCodeRunJSONAndWorkdir(t *testing.T) {
	res, err := opencode.BuildArgs("run-1", "hello", os.TempDir(), map[string]any{
		"model_selector": "anthropic/claude-opus-4-7",
		"agent":          "build",
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--format", "json") || !slices.Contains(res.Args, "run") {
		t.Fatalf("args missing run/json: %v", res.Args)
	}
	if !containsPair(res.Args, "--dir", os.TempDir()) {
		t.Fatalf("args missing --dir temp: %v", res.Args)
	}
	if !containsPair(res.Args, "--model", "anthropic/claude-opus-4-7") {
		t.Fatalf("args missing model selector: %v", res.Args)
	}
	if !containsPair(res.Args, "--agent", "build") {
		t.Fatalf("args missing agent: %v", res.Args)
	}
	if got := res.Args[len(res.Args)-1]; got != "hello" {
		t.Fatalf("last arg prompt = %q, want hello; args=%v", got, res.Args)
	}
}

func TestBuildArgsRejectsRelativeWorkdir(t *testing.T) {
	_, err := opencode.BuildArgs("run-1", "hello", "./relative", nil)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("BuildArgs relative err = %v, want absolute-path error", err)
	}
}

// TestBuildArgsCreatesMissingWorkdir: align with claudecode + codex —
// a non-existent absolute path is mkdir -p'd. Pinning this keeps the
// wizard's "Created if it does not exist" hint honest across engines.
func TestBuildArgsCreatesMissingWorkdir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "parents", "leaf")
	res, err := opencode.BuildArgs("run-1", "hello", target, nil)
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--dir", target) {
		t.Fatalf("args missing --dir %s: %v", target, res.Args)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target %q is not a directory", target)
	}
}

func TestBuildArgsWritesManagedConfigUnderParsarHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARSAR_HOME", home)
	res, err := opencode.BuildArgs("run/id", "hello", "", map[string]any{
		"opencode_json": `{"provider":{},"permission":{"*":"allow"}}`,
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	configHome := envValue(res.Env, "XDG_CONFIG_HOME")
	if configHome == "" {
		t.Fatalf("XDG_CONFIG_HOME missing in env: %v", res.Env)
	}
	wantPrefix := filepath.Join(home, "parsar-daemon", "scratch", "run_id", "config-home")
	if configHome != wantPrefix {
		t.Fatalf("configHome = %q, want %q", configHome, wantPrefix)
	}
	configPath := filepath.Join(configHome, "opencode", "opencode.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected opencode.json at %s: %v", configPath, err)
	}
	res.Cleanup()
	if _, err := os.Stat(filepath.Join(home, "parsar-daemon", "scratch", "run_id")); !os.IsNotExist(err) {
		t.Fatalf("scratch dir still exists after cleanup: %v", err)
	}
}

func TestBuildArgsMergesLocalAndRemoteMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARSAR_HOME", home)
	res, err := opencode.BuildArgs("run-mcp", "hello", "", map[string]any{
		"opencode_json": `{"provider":{}}`,
		"mcp_servers": map[string]any{
			"local": map[string]any{"command": "npx", "args": []any{"-y", "pkg"}},
			"docs": map[string]any{
				"url":     "https://docs.example.com/mcp",
				"headers": map[string]any{"Authorization": "Bearer token"},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	path := filepath.Join(envValue(res.Env, "XDG_CONFIG_HOME"), "opencode", "opencode.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	mcp := config["mcp"].(map[string]any)
	remote := mcp["docs"].(map[string]any)
	if remote["type"] != "remote" || remote["url"] != "https://docs.example.com/mcp" {
		t.Fatalf("remote = %+v", remote)
	}
	headers := remote["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer token" {
		t.Fatalf("headers = %+v", headers)
	}
	local := mcp["local"].(map[string]any)
	if local["type"] != "local" {
		t.Fatalf("local = %+v", local)
	}
}

func TestBuildArgsRejectsBadEnvShape(t *testing.T) {
	_, err := opencode.BuildArgs("run-1", "hello", "", map[string]any{"env": map[string]any{"K": 1}})
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("BuildArgs env err = %v, want env shape error", err)
	}
}

func containsPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
