package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// BuildResult is the opencode CLI launch plan for one prompt.
type BuildResult struct {
	Args    []string
	Env     []string
	WorkDir string
	Cleanup func()
}

// BuildArgs translates the daemon prompt_request into an `opencode
// run` invocation.
func BuildArgs(runID, prompt, workDir string, opts map[string]any) (BuildResult, error) {
	cleanup := func() {}
	result := BuildResult{Cleanup: cleanup}

	resolvedWorkDir, err := resolveWorkDir(workDir)
	if err != nil {
		return result, err
	}

	promptText, err := buildPrompt(prompt, opts)
	if err != nil {
		return result, err
	}

	args := []string{"run", "--format", "json"}
	if resolvedWorkDir != "" {
		args = append(args, "--dir", resolvedWorkDir)
	}
	if model := firstString(opts, "model_selector", "model"); model != "" {
		args = append(args, "--model", model)
	}
	if agent := stringOpt(opts, "agent"); agent != "" {
		args = append(args, "--agent", agent)
	}
	if boolOpt(opts, "dangerously_skip_permissions") {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, promptText)

	env, err := buildEnv(opts)
	if err != nil {
		return result, err
	}

	rawConfig := stringOpt(opts, "opencode_json")
	if servers, ok := opts["mcp_servers"]; ok && servers != nil {
		rawConfig, err = mergeMCPConfig(rawConfig, servers)
		if err != nil {
			return result, err
		}
	}
	if rawConfig != "" {
		configHome, scratchCleanup, err := writeConfigHome(runID, rawConfig)
		if err != nil {
			return result, err
		}
		cleanup = scratchCleanup
		env = append(env, "XDG_CONFIG_HOME="+configHome)
	}

	result.Args = args
	result.Env = env
	result.WorkDir = resolvedWorkDir
	result.Cleanup = cleanup
	return result, nil
}

func mergeMCPConfig(rawConfig string, rawServers any) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(rawConfig) != "" {
		if err := json.Unmarshal([]byte(rawConfig), &config); err != nil {
			return "", fmt.Errorf("opencode: opencode_json must be valid JSON: %w", err)
		}
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return "", fmt.Errorf("opencode: mcp_servers must be object, got %T", rawServers)
	}
	mcp, _ := config["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	for name, raw := range servers {
		entry, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("opencode: mcp_servers[%q] must be object, got %T", name, raw)
		}
		enabled := true
		if value, ok := entry["enabled"].(bool); ok {
			enabled = value
		}
		if remoteURL, ok := entry["url"].(string); ok && strings.TrimSpace(remoteURL) != "" {
			mcp[name] = map[string]any{
				"type":    "remote",
				"url":     strings.TrimSpace(remoteURL),
				"enabled": enabled,
			}
			continue
		}
		command, ok := entry["command"].(string)
		if !ok || strings.TrimSpace(command) == "" {
			return "", fmt.Errorf("opencode: mcp_servers[%q] missing command or url", name)
		}
		commandParts := []string{command}
		if args, ok := entry["args"].([]any); ok {
			for _, arg := range args {
				if value, ok := arg.(string); ok {
					commandParts = append(commandParts, value)
				}
			}
		} else if args, ok := entry["args"].([]string); ok {
			commandParts = append(commandParts, args...)
		}
		local := map[string]any{"type": "local", "command": commandParts, "enabled": enabled}
		if env, ok := entry["env"].(map[string]any); ok && len(env) > 0 {
			local["environment"] = env
		} else if env, ok := entry["env"].(map[string]string); ok && len(env) > 0 {
			local["environment"] = env
		}
		mcp[name] = local
	}
	if len(mcp) > 0 {
		config["mcp"] = mcp
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("opencode: marshal merged MCP config: %w", err)
	}
	return string(encoded), nil
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
			return "", fmt.Errorf("opencode: resolve home dir: %w", err)
		}
		abs = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	case filepath.IsAbs(trimmed):
		abs = trimmed
	default:
		return "", fmt.Errorf("opencode: work_dir must be absolute or start with ~/, got %q", trimmed)
	}
	// Match claudecode + codex: mkdir -p so the wizard's "Created if
	// it does not exist" hint actually holds across engines.
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("opencode: mkdir work_dir %s: %w", abs, err)
	}
	return abs, nil
}

func buildPrompt(prompt string, opts map[string]any) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("opencode: empty prompt")
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

func buildEnv(opts map[string]any) ([]string, error) {
	env := []string{
		"DISABLE_TELEMETRY=1",
	}
	raw, ok := opts["env"]
	if !ok || raw == nil {
		return env, nil
	}
	envMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("opencode.BuildArgs: env must be object, got %T", raw)
	}
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := envMap[k].(string)
		if !ok {
			return nil, fmt.Errorf("opencode.BuildArgs: env[%q] must be string, got %T", k, envMap[k])
		}
		env = append(env, k+"="+s)
	}
	return env, nil
}

func writeConfigHome(runID, raw string) (string, func(), error) {
	if strings.TrimSpace(runID) == "" {
		return "", func() {}, fmt.Errorf("opencode: runID required for scratch config")
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", func() {}, fmt.Errorf("opencode: opencode_json must be valid JSON: %w", err)
	}
	root, err := paths.Root()
	if err != nil {
		return "", func() {}, err
	}
	scratchRoot := filepath.Join(root, "parsar-daemon", "scratch", safeRunID(runID))
	configHome := filepath.Join(scratchRoot, "config-home")
	opencodeDir := filepath.Join(configHome, "opencode")
	if err := os.MkdirAll(opencodeDir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("opencode: create config dir %s: %w", opencodeDir, err)
	}
	configPath := filepath.Join(opencodeDir, "opencode.json")
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		return "", func() {}, fmt.Errorf("opencode: write %s: %w", configPath, err)
	}
	cleanup := func() { _ = os.RemoveAll(scratchRoot) }
	return configHome, cleanup, nil
}

func safeRunID(runID string) string {
	var b strings.Builder
	for _, r := range runID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "run"
	}
	return out
}

func firstString(opts map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringOpt(opts, key); v != "" {
			return v
		}
	}
	return ""
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

func boolOpt(opts map[string]any, key string) bool {
	if opts == nil {
		return false
	}
	v, ok := opts[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}
