// Package opencode renders a Parsar ModelRuntime + decrypted API key
// into the opencode.json provider/model config opencode expects, written
// under a per-run XDG_CONFIG_HOME the caller exports and cleans up.
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// MCPServerSpec is one entry of opencode's `mcp` config block; Parsar
// only emits the `local` flavour (opencode spawns the server as a stdio
// subprocess). Environment is where the connector inlines decrypted
// Action Credential values — this package never sees raw secret refs.
type MCPServerSpec struct {
	Name        string
	Command     []string
	Environment map[string]string
	Enabled     bool
}

// RenderInput holds optional knobs for RenderConfig/Render. Required
// values (modelRuntime, apiKey) stay positional.
type RenderInput struct {
	// PluginSpec, when non-empty, becomes `plugin: [<spec>]` in the
	// rendered opencode.json (npm name OR absolute file path per
	// opencode's loader rules).
	PluginSpec string

	// MCPServers, when non-empty, becomes the `mcp` block. Entries
	// with empty Name or empty Command are silently skipped so
	// callers can build the list with conditional inclusions.
	//
	// Enabled is forwarded verbatim, NOT used as a filter — opencode
	// upstream treats `enabled: false` as "declared but dormant"
	// (config present, subprocess not started) and pre-filtering
	// here would lose that semantic.
	MCPServers []MCPServerSpec
}

// RenderConfig is the pure-JSON half of Render: it builds the
// opencode.json payload and returns the marshalled bytes with no
// filesystem side effects so sandbox callers can stream them straight
// in. Returns (nil, nil) when essential fields (apiKey, adapter,
// provider, model id) are missing so callers can fall back to env-var
// injection.
func RenderConfig(modelRuntime store.ModelRuntime, apiKey string, in RenderInput) ([]byte, error) {
	if apiKey == "" || modelRuntime.ProviderType == "" || modelRuntime.ModelKey == "" || modelRuntime.Adapter == "" {
		return nil, nil
	}

	providerKey := modelRuntime.ProviderType

	providerOptions := map[string]any{"apiKey": apiKey}
	if modelRuntime.BaseURL != "" {
		providerOptions["baseURL"] = modelRuntime.BaseURL
	}
	if extra, ok := modelRuntime.ProviderConfig["options"].(map[string]any); ok {
		for k, v := range extra {
			providerOptions[k] = v
		}
	}

	modelBlock := map[string]any{
		"name": firstNonEmpty(modelRuntime.ModelName, modelRuntime.ModelKey),
	}
	// Default capability flags to true; explicit values (even false) win.
	for _, key := range []string{"reasoning", "tool_call", "temperature", "attachment"} {
		if v, ok := modelRuntime.Capabilities[key]; ok {
			modelBlock[key] = v
		} else {
			modelBlock[key] = true
		}
	}
	if len(modelRuntime.Limits) > 0 {
		modelBlock["limit"] = modelRuntime.Limits
	}
	// headers/modalities can be set at either level; model entries
	// override provider entries per key.
	for _, key := range []string{"headers", "modalities"} {
		base, _ := modelRuntime.ProviderConfig[key].(map[string]any)
		over, _ := modelRuntime.ModelConfig[key].(map[string]any)
		if len(base) == 0 && len(over) == 0 {
			continue
		}
		merged := make(map[string]any, len(base)+len(over))
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range over {
			merged[k] = v
		}
		modelBlock[key] = merged
	}
	if v, ok := modelRuntime.ModelConfig["options"].(map[string]any); ok {
		modelBlock["options"] = v
	}

	providerBlock := map[string]any{
		"name":      providerKey,
		"npm":       modelRuntime.Adapter,
		"options":   providerOptions,
		"models":    map[string]any{modelRuntime.ModelKey: modelBlock},
		"whitelist": []string{modelRuntime.ModelKey},
	}

	config := map[string]any{
		"$schema":  "https://opencode.ai/config.json",
		"provider": map[string]any{providerKey: providerBlock},
	}
	// Parsar does not route opencode's native approval prompts; keep
	// allow mode even with a plugin injected so runtime-local approval
	// cannot stall on a deleted endpoint.
	pluginSpec := strings.TrimSpace(in.PluginSpec)
	config["permission"] = map[string]any{"*": "allow"}
	if pluginSpec != "" {
		config["plugin"] = []string{pluginSpec}
	}

	if len(in.MCPServers) > 0 {
		mcp := make(map[string]any, len(in.MCPServers))
		for _, srv := range in.MCPServers {
			name := strings.TrimSpace(srv.Name)
			if name == "" || len(srv.Command) == 0 {
				continue
			}
			entry := map[string]any{
				"type":    "local",
				"command": append([]string(nil), srv.Command...),
				"enabled": srv.Enabled,
			}
			if len(srv.Environment) > 0 {
				envCopy := make(map[string]string, len(srv.Environment))
				for k, v := range srv.Environment {
					envCopy[k] = v
				}
				entry["environment"] = envCopy
			}
			mcp[name] = entry
		}
		if len(mcp) > 0 {
			config["mcp"] = mcp
		}
	}

	return json.MarshalIndent(config, "", "  ")
}

// Render writes the opencode.json under a per-run XDG_CONFIG_HOME and
// returns its path plus a cleanup func. When essential fields are
// missing it returns ("", noopCleanup, nil) so callers can fall back
// to env-var injection. runID is sanitized into a filesystem-safe slug
// before use.
func Render(runID string, modelRuntime store.ModelRuntime, apiKey string, in RenderInput) (string, func(), error) {
	noop := func() {}
	encoded, err := RenderConfig(modelRuntime, apiKey, in)
	if err != nil {
		return "", noop, err
	}
	if encoded == nil {
		return "", noop, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", noop, err
	}
	safeRunID := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(runID)
	runtimeRoot := filepath.Join(home, ".parsar", "runtime", "opencode")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		return "", noop, err
	}
	runRoot, err := os.MkdirTemp(runtimeRoot, safeRunID+"-")
	if err != nil {
		return "", noop, err
	}
	configHome := filepath.Join(runRoot, "config-home")
	configDir := filepath.Join(configHome, "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", noop, err
	}
	if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), encoded, 0o600); err != nil {
		return "", noop, err
	}

	cleanup := func() {
		_ = os.RemoveAll(runRoot)
	}
	return configHome, cleanup, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
