package deepseekharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"gopkg.in/yaml.v3"
)

const (
	// dshHomeEnvVar is dsh's only override for its state root: it holds
	// profiles/, sessions/, settings.yaml and .credentials.yaml. Parsar
	// pins it under ~/.parsar so a run never writes into the repo
	// checkout or the daemon's CWD.
	dshHomeEnvVar = "DSH_HOME"

	// dshPermissionModeEnvVar drives dsh's sandbox-policy row. Pinning it
	// keeps the file-effect boundary independent of what a developer-preview
	// release ships as its default.
	dshPermissionModeEnvVar = "DSH_PERMISSION_MODE"

	// sandboxPermissionMode keeps bash and filesystem mutations inside the
	// run's workspace. Parsar runs dsh unattended, so a wider mode would
	// let a turn write outside the directory the operator bound.
	sandboxPermissionMode = "workspace-write"

	// unattendedPreset is the permission preset the overlay declares and
	// selects: workspace-scoped writes with approval prompts off.
	unattendedPreset = "parsar-unattended"

	// dshTelemetryDisabledEnvVar is dsh's authoritative hard opt-out: any
	// non-empty value wins over the composed telemetry row.
	dshTelemetryDisabledEnvVar = "DSH_TELEMETRY_DISABLED"

	// headlessProfile is the shipped one-shot profile (dsh-base +
	// dsh-headless). It auto-initialises from the installation template
	// on first use, so a fresh DSH_HOME needs no provisioning step.
	headlessProfile = "headless"

	// managedRoute is the llm-pi-ai provider route the daemon
	// materialises for a Parsar-managed model, mirroring the "parsar"
	// slug the codex and pi adapters already use.
	managedRoute = "parsar"

	// shippedProviderRoute is dsh-base's own DeepSeek route. It is the
	// provider a model-only agent_options selection has to name, because
	// agent-default-model.provider must match a live llm route.
	shippedProviderRoute = "deepseek-official"
)

// providerConfig is the normalised form of agent_options["dsh_provider"],
// which the server emits for a Parsar-managed model.
type providerConfig struct {
	Name      string
	BaseURL   string
	API       string
	APIKeyEnv string
	Model     string
	Headers   map[string]string
}

type patchRow struct {
	ID     string `yaml:"id"`
	Config any    `yaml:"config"`
}

type piAiConfig struct {
	Providers map[string]piAiRoute `yaml:"providers"`
}

// piAiRoute is one llm-pi-ai provider profile. The field set is dsh's, not
// pi's: apiKeyEnv is a bare env-var name (pi's models.json needs a "$NAME"
// template instead), and there is no auth-header knob because the adapter
// hands the resolved key to pi-ai, whose provider owns the wire auth form.
type piAiRoute struct {
	DisplayName string            `yaml:"displayName,omitempty"`
	APIKeyEnv   string            `yaml:"apiKeyEnv"`
	API         string            `yaml:"api"`
	BaseURL     string            `yaml:"baseURL"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	Models      []piAiModel       `yaml:"models"`
}

type piAiModel struct {
	ID string `yaml:"id"`
}

type defaultModelConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// permissionConfig replaces dsh's permission-preset table. The sandbox mode
// and approval policy cannot be patched independently: dsh validates the
// composed pair against this table (an unmatched pair fails boot with
// "match no preset") and re-pins both knobs from defaultPreset every time a
// session is created, so the unattended pairing has to arrive as a preset.
type permissionConfig struct {
	Presets       map[string]permissionPreset `yaml:"presets"`
	DefaultPreset string                      `yaml:"defaultPreset"`
}

type permissionPreset struct {
	Sandbox     string `yaml:"sandbox"`
	Approval    string `yaml:"approval"`
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// renderPatch builds the `--patch` overlay for one headless prompt. The
// overlay is the last layer dsh applies, and a patch replaces the
// addressed row's whole config rather than merging into it.
func renderPatch(cfg providerConfig, hasProvider bool, model, provider string) ([]byte, error) {
	rows, err := overrideRows(cfg, hasProvider, model, provider)
	if err != nil {
		return nil, err
	}
	body, err := yaml.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("deepseekharness: marshal patch overlay: %w", err)
	}
	return body, nil
}

// overrideRows is the permission / model / route triple both dsh surfaces
// need. The headless path ships it as a `--patch` overlay; the resident
// server path embeds it in the generated profile's own patch layer. It
// lives in one function so the two surfaces cannot drift on the
// unattended permission pairing or the managed-model route.
func overrideRows(cfg providerConfig, hasProvider bool, model, provider string) ([]patchRow, error) {
	// A daemon run has no human answerer for dsh's approval seam, so the
	// shipped `ask` policy would stall every tool call that asks. Writes
	// still stay inside the run's workspace.
	rows := []patchRow{{
		ID: "permission",
		Config: permissionConfig{
			DefaultPreset: unattendedPreset,
			Presets: map[string]permissionPreset{
				unattendedPreset: {
					Sandbox:     sandboxPermissionMode,
					Approval:    "never",
					Name:        "Parsar unattended",
					Description: "Workspace-scoped writes with no approval prompts.",
				},
			},
		},
	}}

	switch {
	case hasProvider:
		if err := validateProvider(cfg); err != nil {
			return nil, err
		}
		// Replacing the llm-pi-ai row's config drops nothing: dsh-base
		// mounts that adapter dormant with no config of its own, and
		// routes come from whichever layer supplies them.
		rows = append(rows,
			patchRow{ID: "llm-pi-ai", Config: piAiConfig{Providers: map[string]piAiRoute{
				managedRoute: {
					DisplayName: cfg.Name,
					APIKeyEnv:   cfg.APIKeyEnv,
					API:         cfg.API,
					BaseURL:     cfg.BaseURL,
					Headers:     cfg.Headers,
					Models:      []piAiModel{{ID: cfg.Model}},
				},
			}}},
			patchRow{ID: "agent-default-model", Config: defaultModelConfig{
				Provider: managedRoute,
				Model:    cfg.Model,
			}},
		)
	case model != "":
		route := provider
		if route == "" {
			route = shippedProviderRoute
		}
		rows = append(rows, patchRow{ID: "agent-default-model", Config: defaultModelConfig{
			Provider: route,
			Model:    model,
		}})
	}
	return rows, nil
}

func validateProvider(cfg providerConfig) error {
	// A route pi-ai does not ship must declare api, baseURL and a
	// non-empty model list or dsh refuses the whole profile at boot.
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("deepseekharness: provider base_url is required")
	}
	if strings.TrimSpace(cfg.API) == "" {
		return fmt.Errorf("deepseekharness: provider api is required")
	}
	if strings.TrimSpace(cfg.APIKeyEnv) == "" {
		return fmt.Errorf("deepseekharness: provider api_key_env is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("deepseekharness: provider model is required")
	}
	return nil
}

// normaliseProvider flattens agent_options["dsh_provider"] into a typed
// providerConfig. hasProvider=false means the key was absent, so the run
// falls back to whatever credentials and model dsh resolves itself.
func normaliseProvider(raw any) (providerConfig, bool, error) {
	if raw == nil {
		return providerConfig{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return providerConfig{}, false, fmt.Errorf("deepseekharness: dsh_provider must be object, got %T", raw)
	}
	cfg := providerConfig{
		Name:      stringOpt(m, "name"),
		BaseURL:   stringOpt(m, "base_url"),
		API:       stringOpt(m, "api"),
		APIKeyEnv: stringOpt(m, "api_key_env"),
		Model:     stringOpt(m, "model"),
	}
	if headers, ok := m["headers"].(map[string]any); ok {
		cfg.Headers = make(map[string]string, len(headers))
		for k, v := range headers {
			if s, ok := v.(string); ok {
				cfg.Headers[k] = s
			}
		}
	}
	return cfg, true, nil
}

// resolveHome returns the DSH_HOME for this prompt. AgentStateKey is
// preferred because it scopes by conversation, agent and engine;
// conversation/run fallbacks exist for older callers and tests.
//
// One home is shared by every run of a state key so the profile is
// initialised once and session logs stay grouped per conversation. Two
// concurrent first runs of the same key therefore both trigger dsh's
// first-use profile initialisation; sequential turns are the normal case
// and a per-run home would re-provision the profile on every prompt.
func resolveHome(agentStateKey, conversationID, runID string) (string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", fmt.Errorf("deepseekharness: resolve state root: %w", err)
	}
	base := filepath.Join(root, "runtime", "deepseek-harness")
	if key := strings.TrimSpace(agentStateKey); key != "" {
		parts := paths.StateKeyParts(key)
		if len(parts) == 0 {
			return "", fmt.Errorf("deepseekharness: invalid agentStateKey %q", agentStateKey)
		}
		dirParts := append([]string{base, "state"}, parts...)
		return filepath.Join(append(dirParts, "home")...), nil
	}
	if id := strings.TrimSpace(conversationID); id != "" {
		return filepath.Join(base, "conv-"+id, "home"), nil
	}
	return filepath.Join(base, "run-"+strings.TrimSpace(runID), "home"), nil
}

// writeRuntimePatch materialises the overlay for one run and returns its
// path plus a cleanup that removes it. The file is run-scoped rather than
// written to $DSH_HOME/cordis.patch.yml because dsh watches the home
// layer for live edits: a concurrent run of the same conversation would
// otherwise re-apply its own model onto an already-booted process.
func writeRuntimePatch(home, runID string, cfg providerConfig, hasProvider bool, model, provider string) (string, func(), error) {
	noop := func() {}
	body, err := renderPatch(cfg, hasProvider, model, provider)
	if err != nil {
		return "", noop, err
	}
	dir := filepath.Join(home, "patches")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", noop, fmt.Errorf("deepseekharness: mkdir patch dir %s: %w", dir, err)
	}
	name := paths.SafePathPart(runID)
	if name == "" {
		name = "run"
	}
	path := filepath.Join(dir, name+".patch.yml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", noop, fmt.Errorf("deepseekharness: write %s: %w", path, err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}
