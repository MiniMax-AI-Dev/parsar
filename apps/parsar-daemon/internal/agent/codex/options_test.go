package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSessionPlan_DefaultsSilentAndFullAccess(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", nil)
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	if !IsSilent(&plan.ApprovalPolicy) {
		t.Fatalf("default policy must be silent, got %+v", plan.ApprovalPolicy)
	}
	if plan.Sandbox != SandboxDangerFullAcces {
		t.Fatalf("default sandbox = %s, want danger-full-access", plan.Sandbox)
	}
	if plan.Cleanup == nil {
		t.Fatal("Cleanup must be non-nil")
	}
	plan.Cleanup()
}

func TestBuildSessionPlan_AllocsCodexHomeAndEnv(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", map[string]any{
		"env": map[string]any{
			"OPENAI_API_KEY": "sk-test",
		},
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	hasCodexHome := false
	hasOpenAI := false
	hasTelemetry := false
	for _, kv := range plan.Env {
		switch {
		case strings.HasPrefix(kv, "CODEX_HOME="):
			hasCodexHome = true
		case kv == "OPENAI_API_KEY=sk-test":
			hasOpenAI = true
		case kv == "DISABLE_TELEMETRY=1":
			hasTelemetry = true
		}
	}
	if !hasCodexHome {
		t.Fatalf("env missing CODEX_HOME: %+v", plan.Env)
	}
	if !hasOpenAI {
		t.Fatalf("env missing OPENAI_API_KEY: %+v", plan.Env)
	}
	if !hasTelemetry {
		t.Fatalf("env missing DISABLE_TELEMETRY: %+v", plan.Env)
	}
}

func TestBuildSessionPlan_RoutesReasoningSummary(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", map[string]any{
		"reasoning_summary": "detailed",
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if len(plan.ExtraConfig) != 1 {
		t.Fatalf("ExtraConfig = %+v", plan.ExtraConfig)
	}
	kv := plan.ExtraConfig[0]
	if kv[0] != "model_reasoning_summary" {
		t.Fatalf("override key = %q", kv[0])
	}
	if kv[1] != `"detailed"` {
		t.Fatalf("override value = %q (must be TOML-quoted)", kv[1])
	}
}

func TestBuildSessionPlan_OverrideSystemPromptReplacesAppend(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", map[string]any{
		"system_prompt":          "user base",
		"override_system_prompt": "you are pirate",
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.SystemPrompt != "you are pirate" {
		t.Fatalf("SystemPrompt = %q, want %q", plan.SystemPrompt, "you are pirate")
	}
}

func TestBuildSessionPlan_EmptyOverrideKeepsSystemPrompt(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", map[string]any{
		"system_prompt":          "user base",
		"override_system_prompt": "",
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.SystemPrompt != "user base" {
		t.Fatalf("SystemPrompt = %q, want %q (empty override should not clobber)", plan.SystemPrompt, "user base")
	}
}

func TestBuildSessionPlan_RejectsRelativeWorkDir(t *testing.T) {
	_, err := BuildSessionPlan("run-1", "relative/dir", nil)
	if err == nil {
		t.Fatal("relative work_dir must error")
	}
}

// TestBuildSessionPlan_CreatesMissingWorkDir: align with claudecode —
// a non-existent absolute path is mkdir -p'd so a user can pin a fresh
// project root in the agent wizard. Without this, codex agents would
// hard-fail the first turn instead of running.
func TestBuildSessionPlan_CreatesMissingWorkDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "parents", "leaf")
	plan, err := BuildSessionPlan("run-1", target, nil)
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.Cwd != target {
		t.Fatalf("plan.Cwd = %q, want %q", plan.Cwd, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target %q is not a directory", target)
	}
}

func TestBuildSessionPlan_WritesMCPConfig(t *testing.T) {
	plan, err := BuildSessionPlan("run-1", "", map[string]any{
		"mcp_servers": map[string]any{
			"docs": map[string]any{
				"command": "docs-server",
				"args":    []any{"--port", "8080"},
				"env":     map[string]any{"TOKEN": "abc"},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	// Find CODEX_HOME so we can verify the config.toml was written there.
	codexHome := ""
	for _, kv := range plan.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			codexHome = strings.TrimPrefix(kv, "CODEX_HOME=")
			break
		}
	}
	if codexHome == "" {
		t.Fatal("CODEX_HOME not in plan.Env")
	}
	// mcp_config.go writes <codexHome>/config.toml — verify it exists
	// and contains the rendered server.
	bodyBytes, err := os.ReadFile(codexHome + "/config.toml")
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, `[mcp_servers."docs"]`) {
		t.Fatalf("config.toml missing docs server: %s", body)
	}
	if !strings.Contains(body, `command = "docs-server"`) {
		t.Fatalf("config.toml missing command: %s", body)
	}
}

func TestBuildSessionPlan_MissingMCPCommandErrors(t *testing.T) {
	_, err := BuildSessionPlan("run-1", "", map[string]any{
		"mcp_servers": map[string]any{
			"broken": map[string]any{
				"args": []any{"--x"},
				// no command
			},
		},
	})
	if err == nil {
		t.Fatal("missing mcp command must surface as error")
	}
}

func TestFirstUserInput_TrimsAndWrapsAsText(t *testing.T) {
	inputs := FirstUserInput("  hello world  ")
	if len(inputs) != 1 {
		t.Fatalf("len = %d", len(inputs))
	}
	if inputs[0].Type != UserInputText || inputs[0].Text != "hello world" {
		t.Fatalf("input = %+v", inputs[0])
	}
}

func TestFirstUserInput_EmptyReturnsNil(t *testing.T) {
	if got := FirstUserInput("   "); got != nil {
		t.Fatalf("empty prompt must return nil, got %+v", got)
	}
}

func TestStringListOpt_FiltersEmpties(t *testing.T) {
	got := stringListOpt(map[string]any{
		"enable_features": []any{"a", "", "  ", "b"},
	}, "enable_features")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("filter = %+v", got)
	}
}
