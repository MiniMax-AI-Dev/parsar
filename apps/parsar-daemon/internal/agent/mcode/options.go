package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

type launchOptions struct {
	Dir, DataDir, Model, Mode string
	Env                       []string
	MCP                       []map[string]any
}

func prepareOptions(ctx context.Context, req proto.PromptRequestPayload) (launchOptions, error) {
	var result launchOptions
	if len(req.Attachments) > 0 {
		return result, fmt.Errorf("mcode: ACP does not support attachments")
	}
	root, err := agent.ManagedSkillsRoot("mcode", req.AgentStateKey, req.ConversationID, req.RunID)
	if err != nil {
		return result, err
	}
	result.DataDir = filepath.Dir(root)
	result.Dir, err = workDir(req.WorkDir, filepath.Join(result.DataDir, "workspace"))
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(result.DataDir, 0o700); err != nil {
		return result, err
	}
	if req.WorkDir == "" {
		if err := os.MkdirAll(result.Dir, 0o700); err != nil {
			return result, err
		}
	}
	installed, err := claudecode.InstallManagedSkills(ctx, log.With("component", "mcode"), root, req.AgentOptions["skills"])
	if err != nil {
		return result, err
	}
	if len(installed.Warnings) > 0 {
		return result, fmt.Errorf("mcode: one or more configured Skills could not be installed")
	}
	opts := req.AgentOptions
	prompt := optionString(opts, "system_prompt")
	if override := optionString(opts, "override_system_prompt"); override != "" {
		prompt = override
	}
	if len(prompt) > 32*1024 {
		return result, fmt.Errorf("mcode: combined instructions exceed the CLI's 32 KiB limit")
	}
	if err := os.WriteFile(filepath.Join(result.DataDir, "AGENTS.md"), []byte(prompt), 0o600); err != nil {
		return result, err
	}
	config := map[string]any{"logLevel": "error", "skills": map[string]any{"external": map[string]any{"enabled": false}}}
	if provider, ok := opts["mcode_provider"].(map[string]any); ok {
		config["custom_provider"] = map[string]any{"parsar": provider}
	} else {
		return result, fmt.Errorf("mcode: a Parsar-managed model is required")
	}
	result.Model = optionString(opts, "model")
	if result.Model == "" {
		return result, fmt.Errorf("mcode: model is required")
	}
	data, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(result.DataDir, "config.yaml"), data, 0o600); err != nil {
		return result, err
	}
	result.Env = append([]string{}, os.Environ()...)
	if raw := opts["env"]; raw != nil {
		env, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("mcode: env must be an object")
		}
		for key, rawValue := range env {
			value, ok := rawValue.(string)
			if !ok || key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
				return result, fmt.Errorf("mcode: invalid environment entry")
			}
			result.Env = append(result.Env, key+"="+value)
		}
	}
	// The adapter owns the native state location, including after cold resume.
	result.Env = append(result.Env, "MINIMAX_DATA_DIR="+result.DataDir)
	result.Mode = optionString(opts, "mode")
	if result.Mode == "" {
		result.Mode = "auto"
	}
	if result.Mode != "auto" && result.Mode != "default" && result.Mode != "bypassPermissions" {
		return result, fmt.Errorf("mcode: unsupported permission mode")
	}
	result.MCP, err = mcpServers(opts["mcp_servers"])
	return result, err
}

func workDir(raw, fallback string) (string, error) {
	if raw == "" {
		return fallback, nil
	}
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		raw = filepath.Join(home, raw[2:])
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("mcode: working directory must be absolute or start with ~/")
	}
	return filepath.Clean(raw), nil
}

func optionString(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}

func mcpServers(raw any) ([]map[string]any, error) {
	result := []map[string]any{}
	if raw == nil {
		return result, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcode: mcp_servers must be an object")
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcode: invalid MCP server %q", name)
		}
		server := map[string]any{"name": name}
		if url := optionString(entry, "url"); url != "" {
			kind := optionString(entry, "type")
			if kind == "" {
				kind = "http"
			}
			if kind != "http" && kind != "sse" {
				return nil, fmt.Errorf("mcode: unsupported MCP transport %q", kind)
			}
			server["type"] = kind
			server["url"] = url
			headers, err := namedValues(entry["headers"])
			if err != nil {
				return nil, err
			}
			server["headers"] = headers
		} else {
			command := optionString(entry, "command")
			if command == "" {
				return nil, fmt.Errorf("mcode: MCP server %q needs command or URL", name)
			}
			server["command"] = command
			args := entry["args"]
			if args == nil {
				args = []string{}
			}
			server["args"] = args
			env, err := namedValues(entry["env"])
			if err != nil {
				return nil, err
			}
			server["env"] = env
		}
		result = append(result, server)
	}
	return result, nil
}

func namedValues(raw any) ([]map[string]string, error) {
	result := []map[string]string{}
	if raw == nil {
		return result, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcode: MCP headers/env must be an object")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, ok := values[key].(string)
		if !ok {
			return nil, fmt.Errorf("mcode: MCP headers/env values must be strings")
		}
		result = append(result, map[string]string{"name": key, "value": value})
	}
	return result, nil
}
