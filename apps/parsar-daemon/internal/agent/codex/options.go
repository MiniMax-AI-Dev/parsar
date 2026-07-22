package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// SessionPlan holds the resolved per-prompt launch plan derived from
// the daemon's PromptRequestPayload.
type SessionPlan struct {
	// Cwd is the validated working directory passed to codex (and to
	// the spawned app-server). Empty when the caller provided no work_dir.
	Cwd string

	// Env is the full environment slice (KEY=value) to layer onto
	// os.Environ() before spawning. Includes CODEX_HOME, plus any
	// caller-provided OPENAI_API_KEY / CODEX_API_KEY / proxy vars.
	Env []string

	// ExtraConfig is a list of `-c key=value` overrides applied at the
	// app-server CLI. Used to layer model_reasoning_summary etc. without
	// editing config.toml.
	ExtraConfig [][2]string

	// EnableFeatures / DisableFeatures forward to `--enable / --disable`
	// flags. Today empty by default; reserved for future ARC opt-in.
	EnableFeatures  []string
	DisableFeatures []string

	// Model is the slug to request on thread/start. Empty inherits the
	// codex.config.toml default.
	Model string

	// ModelProvider is the slug pinned on thread/start so codex routes
	// the prompt through the [model_providers.<slug>] entry we wrote
	// into <CODEX_HOME>/config.toml. Empty leaves codex on its builtin
	// "openai" provider (only valid when the caller really wants
	// public api.openai.com + OPENAI_API_KEY env), so the normal path is
	// parsarProviderSlug.
	ModelProvider string

	// SystemPrompt is forwarded as developerInstructions on thread/start.
	SystemPrompt string

	// CollaborationMode selects Codex's default or plan tool surface.
	CollaborationMode CollaborationModeKind

	// ApprovalPolicy + Sandbox steer thread/start. Defaults surface human
	// approvals and confine writes to the workspace.
	ApprovalPolicy AskForApproval
	Sandbox        SandboxMode

	// Cleanup is the deferred housekeeping the session must run after the child exits.
	Cleanup func()
}

// BuildSessionPlan derives a SessionPlan from PromptRequestPayload's
// fields. The work_dir / opts shape mirrors how claudecode + opencode
// consume their own opts: a string-keyed map of any.
//
// The agent_options keys this function reads:
//
//	model                 string          codex model slug, e.g. "gpt-5.5"
//	system_prompt         string          forwarded as developerInstructions
//	override_system_prompt string         replaces system_prompt entirely when
//	                                      non-empty (mirrors claudecode/opencode)
//	env                   map[string]any  extra env vars (KEY=string-value)
//	mcp_servers           map[string]any  rendered MCP server config — written
//	                                      to <CODEX_HOME>/config.toml [mcp_servers]
//	codex_provider        map[string]any  full provider config — written to
//	                                      <CODEX_HOME>/config.toml
//	                                      [model_providers.<parsarProviderSlug>].
//	                                      Required for any real prompt; when
//	                                      missing, codex falls back to its
//	                                      builtin "openai" provider which only
//	                                      speaks api.openai.com.
//	                                      Recognised keys: name (string),
//	                                      base_url (string, required when
//	                                      codex_provider is present),
//	                                      bearer_token (string, required),
//	                                      wire_api (string; defaults to
//	                                      "responses"),
//	                                      http_headers (map[string]string),
//	                                      query_params (map[string]string),
//	                                      request_max_retries (number),
//	                                      stream_max_retries (number).
//	reasoning_summary     string          one of auto/concise/detailed/none —
//	                                      routed via -c model_reasoning_summary
//	mode                  string          Codex collaboration mode: default/plan
//	enable_features       []any           string list, forwarded as --enable
//	disable_features      []any           string list, forwarded as --disable
//
// The codex binary itself is resolved via PATH only — there's no
// per-prompt override knob. If a deployment needs a custom binary
// location, set it via the daemon's process environment (PATH /
// codexBinary in sessionConfig) rather than per-call.
//
// Approval / sandbox keys are not exposed to admin yet. Parsar's Inbox is
// the default decision surface; per-agent overrides can be added later.
func BuildSessionPlan(runID, agentStateKey, workDir string, opts map[string]any) (SessionPlan, error) {
	cleanup := func() {}
	plan := SessionPlan{
		ApprovalPolicy: HumanApprovalPolicy(),
		Sandbox:        SandboxWorkspaceWrite,
		Cleanup:        cleanup,
	}

	resolvedCwd, err := resolveWorkDirCodex(workDir)
	if err != nil {
		return plan, err
	}
	plan.Cwd = resolvedCwd

	plan.Model = stringOpt(opts, "model")
	plan.SystemPrompt = stringOpt(opts, "system_prompt")
	if override := stringOpt(opts, "override_system_prompt"); override != "" {
		plan.SystemPrompt = override
	}
	if mode := CollaborationModeKind(stringOpt(opts, "mode")); mode != "" {
		switch mode {
		case CollaborationModeDefault, CollaborationModePlan:
			plan.CollaborationMode = mode
		default:
			return plan, fmt.Errorf("codex: unsupported collaboration mode %q", mode)
		}
	}

	env, err := buildSessionEnv(opts)
	if err != nil {
		return plan, err
	}

	codexHome, err := allocCodexHome(agentStateKey)
	if err != nil {
		return plan, err
	}
	if err := resetGeneratedConfig(codexHome); err != nil {
		return plan, err
	}
	env = append(env, "CODEX_HOME="+codexHome)

	// MCP servers come pre-rendered from server/internal/connector/agentdaemon
	// (capabilityAdditions.MCPServers, rendered via render.TargetCodex)
	// as a map of name → {command,args,env}. Write them into
	// <CODEX_HOME>/config.toml so codex picks them up on startup.
	mcpServers, err := normaliseMCPServers(opts["mcp_servers"])
	if err != nil {
		return plan, err
	}
	if len(mcpServers) > 0 {
		if err := writeCodexMCPConfig(codexHome, mcpServers); err != nil {
			return plan, err
		}
	}

	// codex_provider carries the full ModelProviderInfo the server-side
	// injectCodexManagedModel resolved. When set, we materialise it into
	// the [model_providers.<parsarProviderSlug>] block and pin
	// thread/start.model_provider to that slug, so codex skips its
	// builtin "openai" provider entirely.
	provider, hasProvider, err := normaliseProviderConfig(opts["codex_provider"])
	if err != nil {
		return plan, err
	}
	if hasProvider {
		if err := writeCodexProviderConfig(codexHome, provider); err != nil {
			return plan, err
		}
		plan.ModelProvider = parsarProviderSlug
	}

	plan.Env = env
	plan.Cleanup = cleanup
	plan.ExtraConfig = extraConfigFromOpts(opts)
	if plan.ModelProvider != "" {
		// Pin model_provider at the CLI layer so codex skips its builtin
		// "openai" provider — without this the [model_providers.parsar]
		// block we wrote into config.toml would be loaded but never
		// selected (the default model_provider is "openai").
		plan.ExtraConfig = append(plan.ExtraConfig,
			[2]string{"model_provider", strconv(plan.ModelProvider)})
	}
	plan.EnableFeatures = stringListOpt(opts, "enable_features")
	plan.DisableFeatures = stringListOpt(opts, "disable_features")
	return plan, nil
}

// FirstUserInput translates the prompt text + attachments into the
// turn/start payload. Today only text is honoured; image / file
// attachments arrive as proto.PromptAttachment but aren't surfaced to
// the codex CLI yet — TODO once the daemon writes them to disk.
func FirstUserInput(prompt string) []UserInput {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	return []UserInput{{Type: UserInputText, Text: prompt}}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func resolveWorkDirCodex(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}
	var abs string
	switch {
	case strings.HasPrefix(trimmed, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("codex: resolve home dir: %w", err)
		}
		abs = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	case filepath.IsAbs(trimmed):
		abs = trimmed
	default:
		return "", fmt.Errorf("codex: work_dir must be absolute or start with ~/, got %q", trimmed)
	}
	// Match claudecode's resolveSessionWorkDir: mkdir -p so a user
	// naming a fresh project root in the agent wizard works on first
	// run instead of erroring with "does not exist".
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("codex: mkdir work_dir %s: %w", abs, err)
	}
	return abs, nil
}

func allocCodexHome(agentStateKey string) (string, error) {
	if strings.TrimSpace(agentStateKey) == "" {
		return "", fmt.Errorf("codex: agentStateKey required for CODEX_HOME allocation")
	}
	root, err := paths.Root()
	if err != nil {
		return "", err
	}
	parts := strings.Split(agentStateKey, "/")
	safeParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if safe := safePathPartCodex(part); safe != "" {
			safeParts = append(safeParts, safe)
		}
	}
	if len(safeParts) == 0 {
		return "", fmt.Errorf("codex: invalid agentStateKey %q", agentStateKey)
	}
	dirParts := append([]string{root, "parsar-daemon", "agent-sessions"}, safeParts...)
	dir := filepath.Join(dirParts...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("codex: create CODEX_HOME %s: %w", dir, err)
	}
	return dir, nil
}

func resetGeneratedConfig(codexHome string) error {
	path := filepath.Join(codexHome, "config.toml")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("codex: remove generated config %s: %w", path, err)
	}
	return nil
}

func buildSessionEnv(opts map[string]any) ([]string, error) {
	env := []string{
		"DISABLE_TELEMETRY=1",
	}
	raw, ok := opts["env"]
	if !ok || raw == nil {
		return env, nil
	}
	envMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("codex.BuildSessionPlan: env must be object, got %T", raw)
	}
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, ok := envMap[k].(string)
		if !ok {
			return nil, fmt.Errorf("codex.BuildSessionPlan: env[%q] must be string, got %T", k, envMap[k])
		}
		env = append(env, k+"="+s)
	}
	return env, nil
}

func extraConfigFromOpts(opts map[string]any) [][2]string {
	var out [][2]string
	if rs := stringOpt(opts, "reasoning_summary"); rs != "" {
		// codex app-server has no per-call flag; route via -c override.
		// TOML literal — quoted string keeps shell-safe special chars.
		out = append(out, [2]string{"model_reasoning_summary", strconv(rs)})
	}
	return out
}

func stringListOpt(opts map[string]any, key string) []string {
	if opts == nil {
		return nil
	}
	raw, ok := opts[key]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func normaliseMCPServers(raw any) (map[string]mcpServerConfig, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("codex: mcp_servers must be object, got %T", raw)
	}
	out := make(map[string]mcpServerConfig, len(m))
	for name, v := range m {
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex: mcp_servers[%q] must be object, got %T", name, v)
		}
		srv := mcpServerConfig{Name: name}
		if cmd, ok := entry["command"].(string); ok {
			srv.Command = cmd
		}
		if args, ok := entry["args"].([]any); ok {
			for _, a := range args {
				if s, ok := a.(string); ok {
					srv.Args = append(srv.Args, s)
				}
			}
		}
		if env, ok := entry["env"].(map[string]any); ok {
			srv.Env = make(map[string]string, len(env))
			for k, val := range env {
				if s, ok := val.(string); ok {
					srv.Env[k] = s
				}
			}
		}
		if srv.Command == "" {
			return nil, fmt.Errorf("codex: mcp_servers[%q] missing command", name)
		}
		out[name] = srv
	}
	return out, nil
}

// normaliseProviderConfig flattens agent_options["codex_provider"] (a
// string-keyed map produced by injectCodexManagedModel) into a typed
// providerConfig. Returns hasProvider=false when the key is absent, so
// BuildSessionPlan can skip the TOML write entirely; that path is only
// exercised by tests that don't care about model auth.
//
// base_url + bearer_token are validated by writeCodexProviderConfig
// itself (single source of truth) so this function only normalises
// shapes.
func normaliseProviderConfig(raw any) (providerConfig, bool, error) {
	if raw == nil {
		return providerConfig{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return providerConfig{}, false, fmt.Errorf("codex: codex_provider must be object, got %T", raw)
	}
	cfg := providerConfig{}
	if v, ok := m["name"].(string); ok {
		cfg.Name = v
	}
	if v, ok := m["base_url"].(string); ok {
		cfg.BaseURL = v
	}
	if v, ok := m["bearer_token"].(string); ok {
		cfg.BearerToken = v
	}
	if v, ok := m["wire_api"].(string); ok {
		cfg.WireAPI = v
	}
	if hdrs, ok := m["http_headers"].(map[string]any); ok {
		cfg.HTTPHeaders = make(map[string]string, len(hdrs))
		for k, v := range hdrs {
			if s, ok := v.(string); ok {
				cfg.HTTPHeaders[k] = s
			}
		}
	}
	if params, ok := m["query_params"].(map[string]any); ok {
		cfg.QueryParams = make(map[string]string, len(params))
		for k, v := range params {
			if s, ok := v.(string); ok {
				cfg.QueryParams[k] = s
			}
		}
	}
	cfg.RequestMaxRetries = intOpt(m, "request_max_retries")
	cfg.StreamMaxRetries = intOpt(m, "stream_max_retries")
	return cfg, true, nil
}

// intOpt extracts an integer-shaped value from a map. JSON-decoded
// numbers arrive as float64; tests sometimes pass int directly. Both
// are accepted; non-numeric / missing yields 0.
func intOpt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
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

func safePathPartCodex(runID string) string {
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

// strconv quotes a value as a TOML string. Done by reusing the JSON
// encoder for escape rules — TOML strings accept the same standard
// escape set so this is wire-safe.
func strconv(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}
