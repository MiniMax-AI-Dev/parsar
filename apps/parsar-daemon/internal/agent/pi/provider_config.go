package pi

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// piManagedProviderSlug is the provider key the daemon always writes into
// models.json, and the server pins opts["model"] to "parsar/<modelKey>" so
// pi routes through this entry instead of a built-in provider.
const piManagedProviderSlug = "parsar"

// piAgentDirEnvVar is pi's sole override for its config directory
// (config.ts ENV_AGENT_DIR); pi reads models.json from <dir>/models.json.
const piAgentDirEnvVar = "PI_CODING_AGENT_DIR"

type piProviderConfig struct {
	Name       string
	BaseURL    string
	API        string
	APIKeyEnv  string
	Model      string
	Headers    map[string]string
	AuthHeader bool
}

func writePiModelsJSON(agentDir string, cfg piProviderConfig) error {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("pi: provider base_url is required")
	}
	if strings.TrimSpace(cfg.API) == "" {
		return fmt.Errorf("pi: provider api is required")
	}
	if strings.TrimSpace(cfg.APIKeyEnv) == "" {
		return fmt.Errorf("pi: provider api_key_env is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("pi: provider model is required")
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("pi: mkdir agent dir %s: %w", agentDir, err)
	}

	provider := map[string]any{
		"baseUrl": cfg.BaseURL,
		"api":     cfg.API,
		// pi runs apiKey through resolveConfigValue (resolve-config-value.ts):
		// only a "$NAME" / "${NAME}" template is looked up in process.env; a
		// bare string is treated as a LITERAL key. So the env var name must be
		// written with a "$" prefix, otherwise pi sends "PARSAR_PI_API_KEY"
		// verbatim to the provider and the request 401s.
		"apiKey": "$" + cfg.APIKeyEnv,
		"models": []map[string]any{{"id": cfg.Model}},
	}
	if cfg.Name != "" {
		provider["name"] = cfg.Name
	}
	if len(cfg.Headers) > 0 {
		provider["headers"] = cfg.Headers
	}
	if cfg.AuthHeader {
		provider["authHeader"] = true
	}

	doc := map[string]any{"providers": map[string]any{piManagedProviderSlug: provider}}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("pi: marshal models.json: %w", err)
	}
	path := filepath.Join(agentDir, "models.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("pi: write %s: %w", path, err)
	}
	return nil
}

// normalisePiProvider flattens agent_options["pi_provider"] (the string-keyed
// map injectPiManagedModel emits) into a typed piProviderConfig. Returns
// hasProvider=false when the key is absent so callers skip materialisation.
// Required-field validation lives in writePiModelsJSON (single source).
func normalisePiProvider(raw any) (piProviderConfig, bool, error) {
	if raw == nil {
		return piProviderConfig{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return piProviderConfig{}, false, fmt.Errorf("pi: pi_provider must be object, got %T", raw)
	}
	cfg := piProviderConfig{
		Name:      stringOpt(m, "name"),
		BaseURL:   stringOpt(m, "base_url"),
		API:       stringOpt(m, "api"),
		APIKeyEnv: stringOpt(m, "api_key_env"),
		Model:     stringOpt(m, "model"),
	}
	if v, ok := m["auth_header"].(bool); ok {
		cfg.AuthHeader = v
	}
	if hdrs, ok := m["headers"].(map[string]any); ok {
		cfg.Headers = make(map[string]string, len(hdrs))
		for k, v := range hdrs {
			if s, ok := v.(string); ok {
				cfg.Headers[k] = s
			}
		}
	}
	return cfg, true, nil
}

// resolveAgentDir returns the directory set as PI_CODING_AGENT_DIR for this
// prompt. AgentStateKey is preferred because it scopes by conversation, agent,
// and engine; conversation/run fallbacks exist for older callers and tests.
func resolveAgentDir(agentStateKey, conversationID, runID string) (string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", fmt.Errorf("pi: resolve state root: %w", err)
	}
	base := filepath.Join(root, "runtime", "pi")
	if key := strings.TrimSpace(agentStateKey); key != "" {
		parts := safeStatePathParts(key)
		if len(parts) == 0 {
			return "", fmt.Errorf("pi: invalid agentStateKey %q", agentStateKey)
		}
		dirParts := append([]string{base, "state"}, parts...)
		return filepath.Join(append(dirParts, "agent")...), nil
	}
	if id := strings.TrimSpace(conversationID); id != "" {
		return filepath.Join(base, "conv-"+id, "agent"), nil
	}
	return filepath.Join(base, "run-"+strings.TrimSpace(runID), "agent"), nil
}

func resolveSessionDir(agentDir string) (string, error) {
	sessionDir := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return "", fmt.Errorf("pi: mkdir session dir %s: %w", sessionDir, err)
	}
	return sessionDir, nil
}

// applyPiRuntimeState returns a clone of opts with a stable pi --session-dir.
// When opts["pi_provider"] is present it also writes models.json and injects
// PI_CODING_AGENT_DIR into opts["env"] so buildEnv forwards it.
func applyPiRuntimeState(opts map[string]any, agentStateKey, conversationID, runID string) (map[string]any, error) {
	agentDir, err := resolveAgentDir(agentStateKey, conversationID, runID)
	if err != nil {
		return opts, err
	}
	sessionDir, err := resolveSessionDir(agentDir)
	if err != nil {
		return opts, err
	}

	out := cloneAgentOptions(opts)
	out["session_dir"] = sessionDir

	cfg, ok, err := normalisePiProvider(opts["pi_provider"])
	if err != nil {
		return opts, err
	}
	if !ok {
		return out, nil
	}
	if err := writePiModelsJSON(agentDir, cfg); err != nil {
		return opts, err
	}
	out["env"] = withAgentDirEnv(opts["env"], agentDir)
	return out, nil
}

func safeStatePathParts(key string) []string {
	rawParts := strings.Split(key, "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if safe := safeStatePathPart(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	return parts
}

func safeStatePathPart(part string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(part) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return ""
	}
	return out
}

func withAgentDirEnv(existing any, agentDir string) map[string]any {
	out := map[string]any{}
	if m, ok := existing.(map[string]any); ok {
		maps.Copy(out, m)
	}
	out[piAgentDirEnvVar] = agentDir
	return out
}
