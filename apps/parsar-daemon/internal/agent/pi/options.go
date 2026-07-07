package pi

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildResult is the pi CLI launch plan for one prompt. Cleanup is
// always non-nil so callers can defer it blindly.
type BuildResult struct {
	Args    []string
	Env     []string
	WorkDir string
	Cleanup func()
}

// BuildArgs translates the daemon prompt_request into a `pi --mode json`
// invocation. resumeSessionID, if non-empty, takes precedence over any
// "resume_session_id" key in opts. pi has no working-directory flag, so
// WorkDir is resolved here and the caller sets cmd.Dir.
func BuildArgs(runID, prompt, workDir string, opts map[string]any, resumeSessionID string) (BuildResult, error) {
	_ = runID
	result := BuildResult{Cleanup: func() {}}

	resolvedWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return result, err
	}

	promptText, err := buildPrompt(prompt)
	if err != nil {
		return result, err
	}

	// --mode json: machine-readable NDJSON output for the translator.
	// -p: non-interactive (print) mode — process prompt and exit, no
	// interactive trust prompt or TUI.
	args := []string{"--mode", "json"}

	if model := stringOpt(opts, "model"); model != "" {
		args = append(args, "--model", model)
	}
	if provider := stringOpt(opts, "provider"); provider != "" {
		args = append(args, "--provider", provider)
	}
	// A managed api_key is deliberately NOT forwarded as --api-key: secrets
	// ride the environment (PARSAR_PI_API_KEY, referenced from the
	// materialised models.json) so they never land on the pi child's argv,
	// where `ps` would leak them. See server injectPiManagedModel.

	// override replaces the base system prompt and wins over append.
	if override := stringOpt(opts, "override_system_prompt"); override != "" {
		args = append(args, "--system-prompt", override)
	} else if sys := stringOpt(opts, "system_prompt"); sys != "" {
		args = append(args, "--append-system-prompt", sys)
	}

	resume := strings.TrimSpace(resumeSessionID)
	if resume == "" {
		resume = stringOpt(opts, "resume_session_id")
	}
	if resume != "" {
		args = append(args, "--session", resume)
	}

	skillDirs, err := stringSlice(opts["skill_dirs"])
	if err != nil {
		return result, fmt.Errorf("pi.BuildArgs: skill_dirs: %w", err)
	}
	for _, d := range skillDirs {
		if d = strings.TrimSpace(d); d != "" {
			args = append(args, "--skill", d)
		}
	}

	// pi's -p consumes the immediately-following arg as the prompt, so it
	// must be appended last, after every other flag.
	args = append(args, "-p", promptText)

	env, err := buildEnv(opts)
	if err != nil {
		return result, err
	}

	result.Args = args
	result.Env = env
	result.WorkDir = resolvedWorkDir
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
			return "", fmt.Errorf("pi: resolve home dir: %w", err)
		}
		abs = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	case filepath.IsAbs(trimmed):
		abs = trimmed
	default:
		return "", fmt.Errorf("pi: work_dir must be absolute or start with ~/, got %q", trimmed)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("pi: mkdir work_dir %s: %w", abs, err)
	}
	return abs, nil
}

func buildPrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("pi: empty prompt")
	}
	return prompt, nil
}

func buildEnv(opts map[string]any) ([]string, error) {
	// PI_TELEMETRY=0 force-disables pi's opt-in install telemetry for
	// unattended daemon runs (pi reads PI_TELEMETRY, not DISABLE_TELEMETRY).
	env := []string{"PI_TELEMETRY=0"}
	raw, ok := opts["env"]
	if !ok || raw == nil {
		return env, nil
	}
	envMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pi.BuildArgs: env must be object, got %T", raw)
	}
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := envMap[k].(string)
		if !ok {
			return nil, fmt.Errorf("pi.BuildArgs: env[%q] must be string, got %T", k, envMap[k])
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

// stringSlice coerces a value to []string, accepting a typed []string or
// the []any json.Unmarshal produces for a JSON array. nil yields nil.
func stringSlice(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for i, el := range x {
			s, ok := el.(string)
			if !ok {
				return nil, fmt.Errorf("element %d must be string, got %T", i, el)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be array of strings, got %T", v)
	}
}
