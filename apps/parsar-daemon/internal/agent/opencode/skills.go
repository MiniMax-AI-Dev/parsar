package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const (
	openCodeConfigDirEnv     = "OPENCODE_CONFIG_DIR"
	openCodeConfigContentEnv = "OPENCODE_CONFIG_CONTENT"
)

func prepareManagedSkills(ctx context.Context, logger *slog.Logger, req proto.PromptRequestPayload) (map[string]any, error) {
	rawSkills, hasSkills := req.AgentOptions["skills"]
	if !hasSkills && strings.TrimSpace(req.AgentStateKey) == "" && strings.TrimSpace(req.ConversationID) == "" && strings.TrimSpace(req.RunID) == "" {
		return req.AgentOptions, nil
	}
	root, err := agent.ManagedSkillsRoot("opencode", req.AgentStateKey, req.ConversationID, req.RunID)
	if err != nil {
		return nil, fmt.Errorf("opencode: resolve managed skills root: %w", err)
	}
	result, err := claudecode.InstallManagedSkills(ctx, logger, root, rawSkills)
	if err != nil {
		return nil, fmt.Errorf("opencode: install skills: %w", err)
	}
	for _, warning := range result.Warnings {
		logger.Warn("opencode: skill install warning", "run_id", req.RunID, "msg", warning)
	}
	if len(result.SkillDirs) == 0 {
		return req.AgentOptions, nil
	}
	return withOpenCodeSkillRoot(req.AgentOptions, root)
}

func withOpenCodeSkillRoot(opts map[string]any, root string) (map[string]any, error) {
	out := make(map[string]any, len(opts)+1)
	for key, value := range opts {
		out[key] = value
	}
	env := map[string]any{}
	if raw := opts["env"]; raw != nil {
		existing, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("opencode.BuildArgs: env must be object, got %T", raw)
		}
		for key, value := range existing {
			env[key] = value
		}
	}
	configDir, err := openCodeEnvValue(env, openCodeConfigDirEnv)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(configDir) == "" {
		env[openCodeConfigDirEnv] = filepath.Dir(root)
	} else {
		inline, err := openCodeEnvValue(env, openCodeConfigContentEnv)
		if err != nil {
			return nil, err
		}
		inline, err = addOpenCodeSkillPath(inline, root)
		if err != nil {
			return nil, err
		}
		env[openCodeConfigContentEnv] = inline
	}
	out["env"] = env
	return out, nil
}

func openCodeEnvValue(env map[string]any, key string) (string, error) {
	if raw, ok := env[key]; ok {
		value, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("opencode.BuildArgs: env[%q] must be string, got %T", key, raw)
		}
		return value, nil
	}
	return os.Getenv(key), nil
}

func addOpenCodeSkillPath(rawConfig, root string) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(rawConfig) != "" {
		if err := json.Unmarshal([]byte(rawConfig), &config); err != nil {
			return "", fmt.Errorf("opencode: %s must be valid JSON: %w", openCodeConfigContentEnv, err)
		}
		if config == nil {
			config = map[string]any{}
		}
	}
	skills, ok := config["skills"].(map[string]any)
	if !ok && config["skills"] != nil {
		return "", fmt.Errorf("opencode: %s skills must be object, got %T", openCodeConfigContentEnv, config["skills"])
	}
	if skills == nil {
		skills = map[string]any{}
	}
	paths := []any{}
	if rawPaths := skills["paths"]; rawPaths != nil {
		var ok bool
		paths, ok = rawPaths.([]any)
		if !ok {
			return "", fmt.Errorf("opencode: %s skills.paths must be array, got %T", openCodeConfigContentEnv, rawPaths)
		}
	}
	found := false
	for _, path := range paths {
		value, ok := path.(string)
		if !ok {
			return "", fmt.Errorf("opencode: %s skills.paths entries must be strings", openCodeConfigContentEnv)
		}
		found = found || value == root
	}
	if found {
		return rawConfig, nil
	}
	skills["paths"] = append(paths, root)
	config["skills"] = skills
	body, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("opencode: marshal %s: %w", openCodeConfigContentEnv, err)
	}
	return string(body), nil
}
