package deepseekharness

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildResult is the dsh CLI launch plan for one prompt. Cleanup is
// always non-nil so callers can defer it blindly.
type BuildResult struct {
	Args    []string
	Env     []string
	WorkDir string
	Cleanup func()
}

// StateKeys carries the identifiers the adapter derives its DSH_HOME and
// per-run patch overlay from.
type StateKeys struct {
	AgentStateKey  string
	ConversationID string
	RunID          string
}

// BuildArgs translates the daemon prompt_request into a
// `dsh --profile headless <task>` invocation.
func BuildArgs(prompt, workDir string, opts map[string]any, state StateKeys) (BuildResult, error) {
	result := BuildResult{Cleanup: func() {}}

	resolvedWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return result, err
	}

	// dsh headless takes the task as one positional argument and offers
	// no --system-prompt flag, so an injected system prompt is prepended
	// to the task text (same as the opencode adapter).
	promptText, err := buildPrompt(prompt, opts)
	if err != nil {
		return result, err
	}

	provider, hasProvider, err := normaliseProvider(opts["dsh_provider"])
	if err != nil {
		return result, err
	}
	home, err := resolveHome(state.AgentStateKey, state.ConversationID, state.RunID)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return result, fmt.Errorf("deepseekharness: mkdir dsh home %s: %w", home, err)
	}
	patchPath, cleanup, err := writeRuntimePatch(home, state.RunID, provider, hasProvider,
		stringOpt(opts, "model"), stringOpt(opts, "provider"))
	if err != nil {
		return result, err
	}

	args := []string{"--profile", headlessProfile, "--patch", patchPath}
	// The launcher consumes one `--`, so everything after it reaches the
	// headless app verbatim — a task starting with a dash included.
	args = append(args, "--", promptText)

	envOpt, err := envMap(opts["env"])
	if err != nil {
		cleanup()
		return result, err
	}
	// Assigned after the caller's env is copied so agent_options cannot
	// redirect the state root, widen the file-effect boundary, or turn
	// telemetry back on for an unattended run.
	envOpt[dshHomeEnvVar] = home
	envOpt[dshPermissionModeEnvVar] = sandboxPermissionMode
	envOpt[dshTelemetryDisabledEnvVar] = "1"
	env, err := buildEnv(envOpt)
	if err != nil {
		cleanup()
		return result, err
	}

	result.Args = args
	result.Env = env
	result.WorkDir = resolvedWorkDir
	result.Cleanup = cleanup
	return result, nil
}

func resolveWorkDir(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}
	var abs string
	switch {
	case strings.HasPrefix(trimmed, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("deepseekharness: resolve home dir: %w", err)
		}
		abs = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	case filepath.IsAbs(trimmed):
		abs = trimmed
	default:
		return "", fmt.Errorf("deepseekharness: work_dir must be absolute or start with ~/, got %q", trimmed)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("deepseekharness: mkdir work_dir %s: %w", abs, err)
	}
	return abs, nil
}

func buildPrompt(prompt string, opts map[string]any) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("deepseekharness: empty prompt")
	}
	systemPrompt := stringOpt(opts, "system_prompt")
	if override := stringOpt(opts, "override_system_prompt"); override != "" {
		systemPrompt = override
	}
	if systemPrompt == "" {
		return prompt, nil
	}
	return systemPrompt + "\n\n" + prompt, nil
}

func envMap(raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("deepseekharness.BuildArgs: env must be object, got %T", raw)
	}
	out := make(map[string]any, len(m)+1)
	maps.Copy(out, m)
	return out, nil
}

func buildEnv(envOpt map[string]any) ([]string, error) {
	env := make([]string, 0, len(envOpt))
	keys := make([]string, 0, len(envOpt))
	for k := range envOpt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := envOpt[k].(string)
		if !ok {
			return nil, fmt.Errorf("deepseekharness.BuildArgs: env[%q] must be string, got %T", k, envOpt[k])
		}
		env = append(env, k+"="+s)
	}
	return env, nil
}

func stringOpt(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	v, ok := opts[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
