package deepseekharness_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
)

func stateKeys(runID string) deepseekharness.StateKeys {
	return deepseekharness.StateKeys{AgentStateKey: "conv1/agent1/deepseek_harness", RunID: runID}
}

func TestBuildArgsUsesHeadlessProfileAndTaskLast(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", os.TempDir(), nil, stateKeys("run-1"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()

	if !containsPair(res.Args, "--profile", "headless") {
		t.Fatalf("args missing --profile headless: %v", res.Args)
	}
	// The launcher consumes one `--`, so the task must be the final arg
	// directly behind it or a task starting with a dash is parsed as a
	// launcher flag.
	n := len(res.Args)
	if n < 2 || res.Args[n-2] != "--" || res.Args[n-1] != "hello" {
		t.Fatalf("expected args to end with -- hello, got %v", res.Args)
	}
	if res.WorkDir != os.TempDir() {
		t.Fatalf("WorkDir = %q, want %q", res.WorkDir, os.TempDir())
	}
}

func TestBuildArgsWritesRunScopedPatchCleanedUp(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", "", nil, stateKeys("run-patch"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	patchPath := flagValue(res.Args, "--patch")
	if patchPath == "" {
		t.Fatalf("args missing --patch: %v", res.Args)
	}
	body, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	// A daemon run has no approval answerer, so the overlay must always
	// select the unattended permission preset even without a managed model.
	if !strings.Contains(string(body), "id: permission") || !strings.Contains(string(body), "defaultPreset: parsar-unattended") {
		t.Fatalf("patch missing permission preset override:\n%s", body)
	}
	res.Cleanup()
	if _, err := os.Stat(patchPath); !os.IsNotExist(err) {
		t.Fatalf("patch file must be removed by Cleanup, stat err = %v", err)
	}
}

func TestBuildArgsPinsDshHomeUnderParsarRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	res, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"env": map[string]any{"DSH_HOME": "/tmp/attacker"},
	}, stateKeys("run-home"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()

	home := envValue(res.Env, deepseekharness.HomeEnvVarForTest)
	if !strings.HasPrefix(home, root) {
		t.Fatalf("DSH_HOME = %q, want a path under %q", home, root)
	}
	if strings.Contains(home, "attacker") {
		t.Fatalf("adapter DSH_HOME must win over agent_options env: %q", home)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		t.Fatalf("DSH_HOME %q not created: err=%v", home, err)
	}
}

func TestBuildArgsSystemPromptPrependsToTask(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"system_prompt": "be terse",
	}, stateKeys("run-sys"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	task := res.Args[len(res.Args)-1]
	if task != "be terse\n\nhello" {
		t.Fatalf("task = %q, want system prompt prepended", task)
	}
}

func TestBuildArgsOverrideSystemPromptWins(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"system_prompt":          "be terse",
		"override_system_prompt": "you are root",
	}, stateKeys("run-override"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	task := res.Args[len(res.Args)-1]
	if task != "you are root\n\nhello" {
		t.Fatalf("task = %q, want override prepended", task)
	}
}

func TestBuildArgsKeepsSecretsOffArgv(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"dsh_provider": map[string]any{
			"base_url":    "https://gw.example/v1",
			"api":         "openai-completions",
			"api_key_env": "PARSAR_DSH_API_KEY",
			"model":       "deepseek-v4",
		},
		"env": map[string]any{"PARSAR_DSH_API_KEY": "sk-secret"},
	}, stateKeys("run-secret"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if slices.Contains(res.Args, "sk-secret") {
		t.Fatalf("api key must not reach argv: %v", res.Args)
	}
	if envValue(res.Env, "PARSAR_DSH_API_KEY") != "sk-secret" {
		t.Fatalf("api key must ride the environment: %v", res.Env)
	}
	body, err := os.ReadFile(flagValue(res.Args, "--patch"))
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	if strings.Contains(string(body), "sk-secret") {
		t.Fatalf("patch overlay must reference the env var, not the key:\n%s", body)
	}
}

// The telemetry opt-out and the file-effect boundary are adapter policy for
// an unattended run, so agent_options must not be able to widen either.
func TestBuildArgsForcesTelemetryOptOutAndPermissionMode(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	res, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"env": map[string]any{
			"DSH_TELEMETRY_DISABLED": "",
			"DSH_PERMISSION_MODE":    "danger-full-access",
		},
	}, stateKeys("run-telemetry"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if got := envValue(res.Env, "DSH_TELEMETRY_DISABLED"); got != "1" {
		t.Fatalf("DSH_TELEMETRY_DISABLED = %q, want the forced opt-out; env=%v", got, res.Env)
	}
	if got := envValue(res.Env, "DSH_PERMISSION_MODE"); got != "workspace-write" {
		t.Fatalf("DSH_PERMISSION_MODE = %q, want workspace-write; env=%v", got, res.Env)
	}
	// A single entry per key: cmd.Env resolves duplicates to the last one,
	// so a caller copy left in place could still win.
	if n := envCount(res.Env, "DSH_PERMISSION_MODE"); n != 1 {
		t.Fatalf("DSH_PERMISSION_MODE appears %d times, want exactly 1: %v", n, res.Env)
	}
}

func TestBuildArgsRejectsRelativeWorkdir(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	_, err := deepseekharness.BuildArgs("hello", "./relative", nil, stateKeys("run-rel"))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("BuildArgs relative err = %v, want absolute-path error", err)
	}
}

func TestBuildArgsCreatesMissingWorkdir(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "missing", "parents", "leaf")
	res, err := deepseekharness.BuildArgs("hello", target, nil, stateKeys("run-mkdir"))
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("work dir %q not created: err=%v", target, err)
	}
}

func TestBuildArgsRejectsEmptyPrompt(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	_, err := deepseekharness.BuildArgs("   ", "", nil, stateKeys("run-empty"))
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("BuildArgs empty prompt err = %v, want prompt error", err)
	}
}

func TestBuildArgsRejectsBadEnvShape(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	_, err := deepseekharness.BuildArgs("hello", "", map[string]any{
		"env": map[string]any{"K": 1},
	}, stateKeys("run-badenv"))
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("BuildArgs env err = %v, want env shape error", err)
	}
}

func TestResolveHomeIsStablePerStateKey(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	first, err := deepseekharness.ResolveHomeForTest("conv1/agent1/deepseek_harness", "conv1", "run-a")
	if err != nil {
		t.Fatalf("ResolveHomeForTest: %v", err)
	}
	second, err := deepseekharness.ResolveHomeForTest("conv1/agent1/deepseek_harness", "conv1", "run-b")
	if err != nil {
		t.Fatalf("ResolveHomeForTest: %v", err)
	}
	if first != second {
		t.Fatalf("home must be stable across runs of one state key: %q vs %q", first, second)
	}
	traversal, err := deepseekharness.ResolveHomeForTest("../../etc/passwd", "", "run-c")
	if err != nil {
		t.Fatalf("ResolveHomeForTest traversal: %v", err)
	}
	if strings.Contains(traversal, "..") {
		t.Fatalf("state key must not escape the root: %q", traversal)
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

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func envCount(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if v, ok := strings.CutPrefix(item, prefix); ok {
			return v
		}
	}
	return ""
}
