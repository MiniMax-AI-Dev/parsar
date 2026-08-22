package deepseekharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/skillinstall"
)

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func materializeManagedSkills(ctx context.Context, launch serverLaunch, raw any) error {
	descriptors, warnings := skillinstall.Decode(raw)
	if len(warnings) > 0 {
		return fmt.Errorf("deepseekharness: decode managed skills: %s", strings.Join(warnings, "; "))
	}
	root := filepath.Join(launch.Home, "skills")
	result, err := skillinstall.Install(ctx, nil, root, descriptors)
	if err != nil {
		return fmt.Errorf("deepseekharness: install managed skills: %w", err)
	}
	if len(result.Warnings) > 0 {
		return fmt.Errorf("deepseekharness: install managed skills: %s", strings.Join(result.Warnings, "; "))
	}
	if err := skillinstall.Prune(root, descriptors); err != nil {
		return fmt.Errorf("deepseekharness: reconcile managed skills: %w", err)
	}
	return nil
}

func normaliseMCPRows(raw any, workDir string) ([]pluginRow, error) {
	if raw == nil {
		return nil, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("deepseekharness: mcp_servers must be object, got %T", raw)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]pluginRow, 0, len(names))
	for _, name := range names {
		if !mcpServerNamePattern.MatchString(name) {
			return nil, fmt.Errorf("deepseekharness: invalid MCP server name %q", name)
		}
		entry, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("deepseekharness: mcp_servers[%q] must be object, got %T", name, servers[name])
		}
		config, err := normaliseMCPConfig(name, entry, workDir)
		if err != nil {
			return nil, err
		}
		rows = append(rows, pluginRow{
			ID:     "parsar-mcp-" + name,
			Name:   "@deepseek-ai/dsh-mcp-client",
			Config: config,
		})
	}
	return rows, nil
}

func normaliseMCPConfig(name string, entry map[string]any, workDir string) (any, error) {
	url := stringOpt(entry, "url")
	command := stringOpt(entry, "command")
	if url != "" && command != "" {
		return nil, fmt.Errorf("deepseekharness: mcp_servers[%q] cannot set both command and url", name)
	}
	if url != "" {
		headers, err := stringMap(entry["headers"])
		if err != nil {
			return nil, fmt.Errorf("deepseekharness: mcp_servers[%q].headers: %w", name, err)
		}
		return mcpHTTPConfig{
			Transport:          "streamable-http",
			ServerName:         name,
			URL:                url,
			Headers:            headers,
			ToolCallTimeoutMS:  60_000,
			FailOnStartupError: true,
		}, nil
	}
	if command == "" {
		return nil, fmt.Errorf("deepseekharness: mcp_servers[%q] missing command or url", name)
	}
	args, err := stringList(entry["args"])
	if err != nil {
		return nil, fmt.Errorf("deepseekharness: mcp_servers[%q].args: %w", name, err)
	}
	env, err := stringMap(entry["env"])
	if err != nil {
		return nil, fmt.Errorf("deepseekharness: mcp_servers[%q].env: %w", name, err)
	}
	return mcpStdioConfig{
		Transport:          "stdio",
		ServerName:         name,
		Command:            command,
		Args:               args,
		Env:                env,
		CWD:                workDir,
		ToolCallTimeoutMS:  60_000,
		FailOnStartupError: true,
	}, nil
}

func stringList(raw any) ([]string, error) {
	if raw == nil {
		return []string{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if typed, ok := raw.([]string); ok {
			return append([]string{}, typed...), nil
		}
		return nil, fmt.Errorf("must be array, got %T", raw)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("item %d must be string, got %T", i, item)
		}
		out = append(out, value)
	}
	return out, nil
}

func stringMap(raw any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	if typed, ok := raw.(map[string]string); ok {
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be object, got %T", raw)
	}
	out := make(map[string]string, len(values))
	for key, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be string, got %T", key, rawValue)
		}
		out[key] = value
	}
	return out, nil
}

func fingerprintMCPRows(rows []pluginRow) (string, error) {
	body, err := json.Marshal(rows)
	if err != nil {
		return "", errors.New("marshal MCP rows")
	}
	return string(body), nil
}
