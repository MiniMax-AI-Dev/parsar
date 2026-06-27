package pi_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
)

func TestBuildArgsUsesModeJsonNoApproveAndPromptLast(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", os.TempDir(), map[string]any{
		"model":    "anthropic/claude-opus-4-7",
		"api_key":  "sk-test",
		"provider": "anthropic",
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()

	if !containsPair(res.Args, "--mode", "json") {
		t.Fatalf("args missing --mode json: %v", res.Args)
	}
	if !slices.Contains(res.Args, "--no-approve") {
		t.Fatalf("args missing --no-approve: %v", res.Args)
	}
	if !containsPair(res.Args, "--model", "anthropic/claude-opus-4-7") {
		t.Fatalf("args missing --model: %v", res.Args)
	}
	if !containsPair(res.Args, "--api-key", "sk-test") {
		t.Fatalf("args missing --api-key: %v", res.Args)
	}
	if !containsPair(res.Args, "--provider", "anthropic") {
		t.Fatalf("args missing --provider: %v", res.Args)
	}
	// pi's -p consumes the immediately-following arg as the prompt, so the
	// prompt MUST be the final arg, preceded by -p.
	n := len(res.Args)
	if n < 2 || res.Args[n-2] != "-p" || res.Args[n-1] != "hello" {
		t.Fatalf("expected args to end with -p hello, got %v", res.Args)
	}
	if res.WorkDir != os.TempDir() {
		t.Fatalf("WorkDir = %q, want %q", res.WorkDir, os.TempDir())
	}
}

func TestBuildArgsRejectsRelativeWorkdir(t *testing.T) {
	_, err := pi.BuildArgs("run-1", "hello", "./relative", nil, "")
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("BuildArgs relative err = %v, want absolute-path error", err)
	}
}

func TestBuildArgsCreatesMissingWorkdir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "parents", "leaf")
	res, err := pi.BuildArgs("run-1", "hello", target, nil, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if res.WorkDir != target {
		t.Fatalf("WorkDir = %q, want %q", res.WorkDir, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target %q is not a directory", target)
	}
}

func TestBuildArgsSystemPromptAppends(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"system_prompt": "be terse",
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--append-system-prompt", "be terse") {
		t.Fatalf("args missing --append-system-prompt: %v", res.Args)
	}
	if slices.Contains(res.Args, "--system-prompt") {
		t.Fatalf("system_prompt must map to append, not override: %v", res.Args)
	}
}

func TestBuildArgsOverrideSystemPromptReplaces(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"system_prompt":          "be terse",
		"override_system_prompt": "you are root",
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--system-prompt", "you are root") {
		t.Fatalf("args missing --system-prompt override: %v", res.Args)
	}
	// Override wins: the append we tentatively added must be stripped.
	if slices.Contains(res.Args, "--append-system-prompt") {
		t.Fatalf("override must strip the append: %v", res.Args)
	}
}

func TestBuildArgsResumeSessionExplicitWins(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"resume_session_id": "from-opts",
	}, "from-param")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--session", "from-param") {
		t.Fatalf("explicit resume id must win: %v", res.Args)
	}
}

func TestBuildArgsResumeSessionFromOpts(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"resume_session_id": "sess-42",
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--session", "sess-42") {
		t.Fatalf("opts resume id missing: %v", res.Args)
	}
}

func TestBuildArgsSkillDirsRepeatFlag(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"skill_dirs": []any{"/skills/a", "/skills/b"},
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if !containsPair(res.Args, "--skill", "/skills/a") {
		t.Fatalf("args missing first --skill: %v", res.Args)
	}
	if !containsPair(res.Args, "--skill", "/skills/b") {
		t.Fatalf("args missing second --skill: %v", res.Args)
	}
}

func TestBuildArgsTelemetryOptOutEnv(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", nil, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if envValue(res.Env, "PI_TELEMETRY") != "0" {
		t.Fatalf("expected PI_TELEMETRY=0 opt-out, env=%v", res.Env)
	}
}

func TestBuildArgsPassesThroughEnvSorted(t *testing.T) {
	res, err := pi.BuildArgs("run-1", "hello", "", map[string]any{
		"env": map[string]any{"BBB": "2", "AAA": "1"},
	}, "")
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	defer res.Cleanup()
	if envValue(res.Env, "AAA") != "1" || envValue(res.Env, "BBB") != "2" {
		t.Fatalf("env passthrough missing: %v", res.Env)
	}
}

func TestBuildArgsRejectsBadEnvShape(t *testing.T) {
	_, err := pi.BuildArgs("run-1", "hello", "", map[string]any{"env": map[string]any{"K": 1}}, "")
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("BuildArgs env err = %v, want env shape error", err)
	}
}

func TestBuildArgsRejectsEmptyPrompt(t *testing.T) {
	_, err := pi.BuildArgs("run-1", "   ", "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("BuildArgs empty prompt err = %v, want prompt error", err)
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
		if v, ok := strings.CutPrefix(item, prefix); ok {
			return v
		}
	}
	return ""
}
