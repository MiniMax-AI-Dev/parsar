package agentdaemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	runtimeopencode "github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/opencode"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ModelResolver is the minimal platform model surface the daemon path
// needs.
//
//   - ResolveModelRuntime gives the inline_secret view (model + secret
//     payload joined). Rejects credential_ref-mode models — those need
//     a user_id to pick the per-user user_credentials row.
//   - ResolveModelRuntimeForUser is the credential_ref-aware variant.
//   - GetSecretPayload fetches the raw encrypted bytes for an
//     inline_secret-mode model.
type ModelResolver interface {
	ResolveModelRuntime(ctx context.Context, workspaceID, modelID string) (store.ModelRuntime, error)
	ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (store.ModelRuntime, error)
	GetSecretPayload(ctx context.Context, workspaceID, secretID string) (store.SecretPayload, error)
}

var (
	ErrManagedModelResolverMissing     = errors.New("agent_daemon: model resolver is not configured")
	ErrManagedModelSecretsMissing      = errors.New("agent_daemon: secret service is not configured")
	ErrManagedModelUnsupported         = errors.New("agent_daemon: claude_code only supports Anthropic-compatible model providers")
	ErrManagedModelSecretMissing       = errors.New("agent_daemon: configured model has no active API key secret")
	ErrManagedModelConfigInvalid       = errors.New("agent_daemon: opencode managed model requires provider slug, model key, and adapter metadata")
	ErrManagedModelPersonalCredMissing = errors.New("agent_daemon: credential_ref model requires the caller to have configured a personal credential")
	ErrManagedModelUserIDMissing       = errors.New("agent_daemon: credential_ref model requires a caller user_id (ConversationInitiatorID)")
)

// ModelCredentialMissingCapabilityID is the capability_id sentinel used
// when a credential-missing notice came from a credential_ref model
// rather than an MCP capability.
const ModelCredentialMissingCapabilityID = "__model__"

// buildAgentOptions resolves the static agent options plus optional
// Parsar-managed model injection. When a model_id/default_model_id is
// configured, Parsar's model registry is the source of truth and wins over
// hand-written agent_options.model/env values.
//
// After model resolution, enabled Skill capabilities for the project_agent
// are rendered through the daemon-side system-prompt slot so both
// claude_code and opencode receive the same high-level instructions.
func (c *Connector) buildAgentOptions(ctx context.Context, in connector.PromptInput) (map[string]any, error) {
	agentKind := resolveAgentKind(in)
	opts := renderStaticAgentOptions(in)
	c.log.Info("agent_daemon: buildAgentOptions static opts rendered",
		"run_id", in.RunID,
		"agent_kind", agentKind,
		"has_agent_config", in.AgentConfig != nil,
		"has_project_agent_config", in.ProjectAgentConfig != nil,
		"agent_config_model_id", stringFromMap(in.AgentConfig, "model_id"),
		"agent_config_default_model_id", stringFromMap(in.AgentConfig, "default_model_id"),
		"project_agent_config_model_id", stringFromMap(in.ProjectAgentConfig, "model_id"),
		"project_agent_config_default_model_id", stringFromMap(in.ProjectAgentConfig, "default_model_id"),
		"opts_model", stringFromMap(opts, "model"))

	// Resolve capabilities up front so the system_prompt fold runs BEFORE
	// applySpecMemoryInjection. That lets an override system_prompt
	// capability short-circuit spec/memory injection via the existing
	// override_system_prompt guard inside applySpecMemoryInjection.
	additions, err := c.resolveCapabilityAdditions(ctx, in, agentKind)
	if err != nil {
		// Capability resolution failure (DB blip, malformed canonical_spec,
		// missing credential) surfaces as a hard prompt failure so the
		// user knows their capability never made it into the run.
		return nil, err
	}
	c.log.Info("agent_daemon: capability additions resolved",
		"run_id", in.RunID,
		"project_agent_id", in.ProjectAgentID,
		"skill_count", len(additions.Skills),
		"mcp_server_count", len(additions.MCPServers),
		"mcp_server_names", mapKeys(additions.MCPServers),
		"plugin_count", len(additions.Plugins),
		"system_prompt_count", len(additions.SystemPrompts),
		"disabled_capability_count", len(additions.Disabled))

	mergeSystemPromptsIntoOptions(opts, additions.SystemPrompts)
	c.applySpecMemoryInjection(ctx, opts, in)
	if err := c.injectManagedModel(ctx, in, opts, agentKind); err != nil {
		c.log.Error("agent_daemon: injectManagedModel failed", "run_id", in.RunID, "err", err)
		return nil, err
	}
	// Default to bypassPermissions when the agent config does not set
	// a permission mode. Without this, Claude Code prompts for tool
	// approval on every call; the daemon's permission-prompt-tool=stdio
	// path works for interactive CLI use but the web UI does not surface
	// approval buttons, so tools stall until the read loop times out.
	if _, ok := opts["mode"]; !ok {
		opts["mode"] = "bypassPermissions"
	}

	mergeSkillsIntoOptions(opts, additions.Skills)
	mergeMCPServersIntoOptions(opts, additions.MCPServers)
	mergePluginsIntoOptions(opts, additions.Plugins)
	// Surface every Disabled capability as a runtime_error system
	// message so the channel layer can render the credential-form
	// nudge. SystemMessages may be nil on dev / smoke contexts;
	// emitDisabledCapabilityNotices guards on it internally.
	c.emitDisabledCapabilityNotices(ctx, in, additions.Disabled)
	return opts, nil
}

// injectManagedModel writes the Parsar-managed model selection into the
// daemon prompt_request. claude_code receives ANTHROPIC_* env + model flag;
// opencode receives a rendered opencode.json blob plus the model selector.
func (c *Connector) injectManagedModel(ctx context.Context, in connector.PromptInput, opts map[string]any, agentKind string) error {
	agentKind = strings.TrimSpace(agentKind)
	modelID := resolveModelID(in)
	if modelID == "" {
		c.log.Warn("agent_daemon: injectManagedModel skipped — no model_id or default_model_id found",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"agent_config_keys", mapKeys(in.AgentConfig),
			"project_agent_config_keys", mapKeys(in.ProjectAgentConfig))
		return nil
	}
	c.log.Info("agent_daemon: injectManagedModel resolving",
		"run_id", in.RunID,
		"agent_kind", agentKind,
		"model_id", modelID,
		"has_model_resolver", c.modelResolver != nil,
		"has_secrets", c.secrets != nil)
	if c.modelResolver == nil {
		return ErrManagedModelResolverMissing
	}
	if c.secrets == nil {
		return ErrManagedModelSecretsMissing
	}

	// Decision table:
	//   has shared model_credential_binding → ResolveModelRuntime (metadata
	//                                         only); the workspace secret is
	//                                         consumed below.
	//   otherwise                           → ResolveModelRuntimeForUser
	//                                         (per-user for credential_ref;
	//                                         a no-op for inline_secret).
	// store.ResolveModelRuntimeForUser still rejects credential_ref + empty
	// initiator — that surfaces the "缺凭据" notice via the err branch below.
	// Mirrors capability_runtime.resolveCredentialValues: binding wins over
	// initiator presence, never the other way.
	modelBinding, hasModelBinding := ParseModelCredentialBinding(in.AgentConfig, in.ProjectAgentConfig)
	var (
		mr  store.ModelRuntime
		err error
	)
	if hasModelBinding {
		mr, err = c.modelResolver.ResolveModelRuntime(ctx, in.WorkspaceID, modelID)
	} else {
		mr, err = c.modelResolver.ResolveModelRuntimeForUser(ctx, modelID, in.ConversationInitiatorID)
	}
	if err != nil {
		if mr.CredentialMode == "credential_ref" && !hasModelBinding {
			c.emitModelCredentialMissingNotice(ctx, in, mr)
			return fmt.Errorf("%w: model_id=%s user_id=%s: %v",
				ErrManagedModelPersonalCredMissing, modelID, in.ConversationInitiatorID, err)
		}
		return fmt.Errorf("agent_daemon: resolve model %s: %w", modelID, err)
	}

	// Agent-level shared binding promotes a credential_ref model to a
	// shared-secret path: resolve once via the workspace secrets table
	// instead of the caller's user_credentials. Enables public/tenant
	// agents (where callers may not have personal LLM keys configured)
	// and lark guests (no platform user_id at all).

	var apiKey string
	switch {
	case mr.CredentialMode == "credential_ref" && hasModelBinding:
		secret, err := c.modelResolver.GetSecretPayload(ctx, in.WorkspaceID, modelBinding.SecretID)
		if err != nil {
			return fmt.Errorf("agent_daemon: load shared model secret model_id=%s secret_id=%s: %w", modelID, modelBinding.SecretID, err)
		}
		if secret.Status != "active" {
			return fmt.Errorf("%w: secret_id=%s status=%s", ErrManagedModelSecretMissing, modelBinding.SecretID, secret.Status)
		}
		payload, err := c.secrets.Decrypt(secret.EncryptedPayload)
		if err != nil {
			return fmt.Errorf("agent_daemon: decrypt shared model secret model_id=%s secret_id=%s: %w", modelID, modelBinding.SecretID, err)
		}
		if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	case mr.CredentialMode == "credential_ref":
		if strings.TrimSpace(in.ConversationInitiatorID) == "" {
			return fmt.Errorf("%w: model_id=%s", ErrManagedModelUserIDMissing, modelID)
		}
		if len(mr.EncryptedPayload) == 0 {
			c.emitModelCredentialMissingNotice(ctx, in, mr)
			return fmt.Errorf("%w: model_id=%s user_id=%s",
				ErrManagedModelPersonalCredMissing, modelID, in.ConversationInitiatorID)
		}
		payload, err := c.secrets.Decrypt(mr.EncryptedPayload)
		if err != nil {
			return fmt.Errorf("agent_daemon: decrypt user credential for model %s: %w", modelID, err)
		}
		if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	default:
		if strings.TrimSpace(mr.SecretID) == "" {
			return fmt.Errorf("%w: model_id=%s", ErrManagedModelSecretMissing, modelID)
		}
		secret, err := c.modelResolver.GetSecretPayload(ctx, in.WorkspaceID, mr.SecretID)
		if err != nil {
			return fmt.Errorf("agent_daemon: load model secret %s: %w", mr.SecretID, err)
		}
		if secret.Status != "active" {
			return fmt.Errorf("%w: secret_id=%s status=%s", ErrManagedModelSecretMissing, mr.SecretID, secret.Status)
		}
		payload, err := c.secrets.Decrypt(secret.EncryptedPayload)
		if err != nil {
			return fmt.Errorf("agent_daemon: decrypt model secret %s: %w", mr.SecretID, err)
		}
		if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("%w: model_id=%s payload missing api_key/value", ErrManagedModelSecretMissing, modelID)
	}

	switch agentKind {
	case "claude_code":
		if err := injectClaudeManagedModel(opts, modelID, mr, apiKey); err != nil {
			return err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"has_base_url", strings.TrimSpace(mr.BaseURL) != "",
			"env_key_count", len(copyStringAnyMap(opts["env"])))
		return nil
	case "opencode":
		if err := injectOpenCodeManagedModel(opts, modelID, mr, apiKey); err != nil {
			return err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"has_opencode_json", stringFromMap(opts, "opencode_json") != "")
		return nil
	case "codex":
		if err := injectCodexManagedModel(opts, modelID, mr, apiKey); err != nil {
			return err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"env_key_count", len(copyStringAnyMap(opts["env"])))
		return nil
	case "pi":
		if err := injectPiManagedModel(opts, modelID, mr, apiKey); err != nil {
			return err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"pi_model", stringFromMap(opts, "model"))
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAgentKind, agentKind)
	}
}

func injectClaudeManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	if !isAnthropicRuntime(mr) {
		return fmt.Errorf("%w: model_id=%s provider_type=%q adapter=%q", ErrManagedModelUnsupported, modelID, mr.ProviderType, mr.Adapter)
	}

	// Claude Code consumes the model id via --model; the daemon
	// converts agent_options.model into that flag.
	opts["model"] = mr.ModelKey

	env := copyStringAnyMap(opts["env"])
	// Pick env var based on the provider's auth_scheme. Default x-api-key
	// (ANTHROPIC_API_KEY); "bearer" uses Authorization: Bearer
	// (ANTHROPIC_AUTH_TOKEN). When both vars are set Claude Code prefers
	// AUTH_TOKEN, so set only the one matching the scheme.
	authScheme := stringFromMap(mr.ProviderConfig, "auth_scheme")
	if authScheme == "bearer" {
		delete(env, "ANTHROPIC_API_KEY")
		env["ANTHROPIC_AUTH_TOKEN"] = apiKey
	} else {
		delete(env, "ANTHROPIC_AUTH_TOKEN")
		env["ANTHROPIC_API_KEY"] = apiKey
	}
	if strings.TrimSpace(mr.BaseURL) != "" {
		env["ANTHROPIC_BASE_URL"] = strings.TrimSpace(mr.BaseURL)
	}
	if customHeaders := anthropicCustomHeaders(mr.ProviderConfig); customHeaders != "" {
		env["ANTHROPIC_CUSTOM_HEADERS"] = customHeaders
	}
	opts["env"] = env
	return nil
}

func injectOpenCodeManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	providerSlug := strings.TrimSpace(mr.ProviderType)
	modelKey := strings.TrimSpace(mr.ModelKey)
	adapter := strings.TrimSpace(mr.Adapter)
	if providerSlug == "" || modelKey == "" || adapter == "" {
		return fmt.Errorf("%w: model_id=%s provider_slug=%q model_key=%q adapter=%q", ErrManagedModelConfigInvalid, modelID, mr.ProviderType, mr.ModelKey, mr.Adapter)
	}
	configJSON, err := runtimeopencode.RenderConfig(mr, apiKey, runtimeopencode.RenderInput{})
	if err != nil {
		return fmt.Errorf("agent_daemon: render opencode config for model %s: %w", modelID, err)
	}
	if len(configJSON) == 0 {
		return fmt.Errorf("%w: model_id=%s provider_slug=%q model_key=%q adapter=%q", ErrManagedModelConfigInvalid, modelID, mr.ProviderType, mr.ModelKey, mr.Adapter)
	}
	opts["model"] = modelKey
	opts["model_selector"] = modelKey
	opts["opencode_json"] = string(configJSON)
	return nil
}

// injectCodexManagedModel writes the Parsar-managed model selection
// for an agent_kind="codex" prompt_request.
//
// Codex doesn't accept its API key / base URL / custom headers through
// env vars in any general way — its builtin "openai" provider only
// reads OPENAI_API_KEY and only speaks api.openai.com. Anything else
// (corporate gateway, Azure, custom X-Sub-Module headers, alternative
// wire path) MUST go through a `[model_providers.<slug>]` block in
// $CODEX_HOME/config.toml.
//
// So this injector stamps a structured `codex_provider` map into
// agent_options. The daemon adapter materialises that map into a TOML
// provider block on every prompt and pins thread/start.model_provider
// (via `-c model_provider=parsar`) so codex routes through it.
//
// What lands in agent_options:
//
//	model               — mr.ModelKey, forwarded to ThreadStartParams.Model
//	codex_provider:
//	  name              — mr.ModelName (display, falls back to "Parsar")
//	  base_url          — mr.BaseURL (required for any real provider)
//	  bearer_token      — apiKey (the decrypted secret)
//	  wire_api          — "responses" (codex-rs has removed "chat")
//	  http_headers      — flattened from mr.ProviderConfig.headers
//	                      (mirrors anthropicCustomHeaders())
//	  query_params      — flattened from mr.ProviderConfig.query_params
//	                      (Azure uses api-version=...)
//
// Provider gating: codex-rs only speaks OpenAI's Responses API.
// ProviderType must be "openai" / "openai-compatible" / "azure-openai"
// or carry an @ai-sdk/openai* adapter slug. Anything else surfaces
// ErrManagedModelUnsupported so the caller emits a credential-form
// notice that points the admin at the right model.
func injectCodexManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	if !isOpenAICompatibleRuntime(mr) {
		return fmt.Errorf("%w: model_id=%s provider_type=%q adapter=%q",
			ErrManagedModelUnsupported, modelID, mr.ProviderType, mr.Adapter)
	}
	if strings.TrimSpace(mr.BaseURL) == "" {
		// Without a base_url the only sensible target is api.openai.com;
		// codex's builtin "openai" provider already covers that. But the
		// daemon's TOML path needs an explicit base_url to write into
		// model_providers — fail loud rather than emit a half-built block.
		return fmt.Errorf("%w: model_id=%s base_url is required for codex provider injection",
			ErrManagedModelConfigInvalid, modelID)
	}

	opts["model"] = mr.ModelKey

	provider := map[string]any{
		"base_url":     strings.TrimSpace(mr.BaseURL),
		"bearer_token": apiKey,
		"wire_api":     "responses",
	}
	if name := strings.TrimSpace(mr.ModelName); name != "" {
		provider["name"] = name
	}
	if headers := flattenStringMap(mr.ProviderConfig, "headers"); len(headers) > 0 {
		provider["http_headers"] = headers
	}
	if params := flattenStringMap(mr.ProviderConfig, "query_params"); len(params) > 0 {
		provider["query_params"] = params
	}
	opts["codex_provider"] = provider
	return nil
}

// flattenStringMap reads a nested string→string map from
// store.ModelRuntime.ProviderConfig (which is map[string]any).
// Non-string values are dropped — the codex TOML serializer only
// accepts strings for both header and query-param values, so it's
// better to drop than to render `123` and have codex reject the
// config at startup.
func flattenStringMap(providerConfig map[string]any, key string) map[string]string {
	raw, _ := providerConfig[key].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isOpenAICompatibleRuntime mirrors isAnthropicRuntime: a defensive
// allow-list of provider_type / adapter values codex-rs's openai
// provider can speak to.
//
// Azure: codex-rs/codex-api/src/provider.rs recognises *.openai.azure.com
// hosts as a first-class provider — we route that through the same
// codex_provider TOML block but a future change might split it out.
func isOpenAICompatibleRuntime(mr store.ModelRuntime) bool {
	for _, v := range []string{mr.ProviderType, mr.Adapter} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "openai", "openai-compatible", "openai_compatible",
			"azure-openai", "azure_openai",
			"@ai-sdk/openai", "@ai-sdk/openai-compatible", "@ai-sdk/azure":
			return true
		}
	}
	return false
}

// piAPIKeyEnv is the env var the daemon sets to the decrypted secret and
// references from the materialised models.json "parsar" provider's apiKey
// field. Carrying the key by env-var name (never inline, never --api-key)
// keeps it out of the pi child's argv, where `ps` would leak it.
const piAPIKeyEnv = "PARSAR_PI_API_KEY"

// piManagedProviderID is the fixed models.json provider id the daemon
// materialises for every managed pi run, mirroring codex's "parsar" slug.
// opts["model"] is "<piManagedProviderID>/<model_key>".
const piManagedProviderID = "parsar"

// injectPiManagedModel stamps the Parsar-managed model into an
// agent_kind="pi" prompt_request.
//
// pi's builtin providers (anthropic/openai/google) hard-code their upstream
// endpoints, so the old "anthropic/<model>" selector dropped mr.BaseURL and
// sent the platform proxy key to api.anthropic.com → 401 invalid x-api-key.
// Like codex, pi therefore needs a structured provider block the daemon
// materialises into a config file (models.json) rather than relying on
// flags/env alone.
//
// What lands in agent_options:
//
//	model         — "parsar/<model_key>", selecting the materialised provider
//	pi_provider:
//	  base_url    — mr.BaseURL (required; forwarding it is the whole point)
//	  api         — pi wire protocol (anthropic-messages / openai-completions
//	                / google-generative-ai)
//	  api_key_env — piAPIKeyEnv; daemon writes models.json apiKey to reference
//	                this env var, whose value rides opts["env"]
//	  model       — mr.ModelKey, for the provider's models:[{id}] list
//	  name        — mr.ModelName (display, optional)
//	  headers     — flattened mr.ProviderConfig.headers (e.g. X-Sub-Module)
//	  auth_header — true for openai-completions (Bearer); anthropic uses
//	                x-api-key so it is omitted
//	env[piAPIKeyEnv] — the decrypted secret
//
// Provider gating: only the three custom-provider APIs pi documents are
// supported; anything else surfaces ErrManagedModelUnsupported so the caller
// emits a credential-form notice. A missing base_url or model_key is
// ErrManagedModelConfigInvalid — fail loud rather than ship a half-built
// provider the daemon can't serialise. All guards run before any opts
// mutation so a rejection leaves opts clean.
func injectPiManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	api := piAPIProtocol(mr)
	if api == "" {
		return fmt.Errorf("%w: model_id=%s provider_type=%q adapter=%q",
			ErrManagedModelUnsupported, modelID, mr.ProviderType, mr.Adapter)
	}
	modelKey := strings.TrimSpace(mr.ModelKey)
	if modelKey == "" {
		return fmt.Errorf("%w: model_id=%s pi requires a model_key",
			ErrManagedModelConfigInvalid, modelID)
	}
	baseURL := strings.TrimSpace(mr.BaseURL)
	if baseURL == "" {
		return fmt.Errorf("%w: model_id=%s base_url is required for pi provider injection",
			ErrManagedModelConfigInvalid, modelID)
	}

	provider := map[string]any{
		"base_url":    baseURL,
		"api":         api,
		"api_key_env": piAPIKeyEnv,
		"model":       modelKey,
	}
	if name := strings.TrimSpace(mr.ModelName); name != "" {
		provider["name"] = name
	}
	if headers := flattenStringMap(mr.ProviderConfig, "headers"); len(headers) > 0 {
		provider["headers"] = headers
	}
	if api == "openai-completions" {
		provider["auth_header"] = true
	}

	opts["model"] = piManagedProviderID + "/" + modelKey
	opts["pi_provider"] = provider

	env := copyStringAnyMap(opts["env"])
	env[piAPIKeyEnv] = apiKey
	opts["env"] = env
	return nil
}

// piAPIProtocol maps a Parsar provider_type / adapter slug onto the pi
// `api` wire protocol the materialised provider speaks. Empty string means
// unsupported (rejected upstream as ErrManagedModelUnsupported).
func piAPIProtocol(mr store.ModelRuntime) string {
	for _, v := range []string{mr.ProviderType, mr.Adapter} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "anthropic", "anthropic-compatible", "anthropic_compatible", "@ai-sdk/anthropic":
			return "anthropic-messages"
		case "openai", "openai-compatible", "openai_compatible",
			"@ai-sdk/openai", "@ai-sdk/openai-compatible":
			return "openai-completions"
		case "google", "gemini", "google-generative-ai", "google_generative_ai", "@ai-sdk/google":
			return "google-generative-ai"
		}
	}
	return ""
}

// applySpecMemoryInjection appends the SessionStart spec/memory bundle
// onto opts["system_prompt"]. Mutates opts in place.
//
// override_system_prompt is the highest-priority signal — when set,
// injection is skipped entirely so an explicit override fully replaces
// the system prompt.
//
// Fail-soft: render errors are logged and swallowed; we never break an
// agent turn because spec/memory rendering failed.
//
// Skipped when SpecMemory is nil or when WorkspaceID/ConversationInitiatorID
// is empty (no user to scope memories to).
func (c *Connector) applySpecMemoryInjection(ctx context.Context, opts map[string]any, in connector.PromptInput) {
	if c.specMemory == nil {
		return
	}
	if in.WorkspaceID == "" || in.ConversationInitiatorID == "" {
		return
	}
	if stringFromMap(opts, "override_system_prompt") != "" {
		return
	}
	injected, err := c.specMemory.RenderSessionPrompt(ctx, in.WorkspaceID, in.ConversationInitiatorID, in.ProjectID)
	if err != nil {
		c.log.Warn("agent_daemon: spec/memory injection failed; proceeding with un-injected system prompt",
			"err", err.Error(),
			"workspace_id", in.WorkspaceID,
			"run_id", in.RunID)
		return
	}
	if injected == "" {
		return
	}
	base := stringFromMap(opts, "system_prompt")
	if base == "" {
		opts["system_prompt"] = injected
		return
	}
	opts["system_prompt"] = base + "\n\n" + injected
}

// mergeSystemPromptsIntoOptions folds resolved system_prompt capabilities
// into opts. Two modes:
//
//   - override: any override capability replaces the system prompt
//     entirely. All override prompts are joined with "\n\n" into
//     opts["override_system_prompt"]; existing system_prompt and any
//     append capability content are dropped. Standard
//     --system-prompt semantics.
//   - append: prompts are joined with "\n\n" and PREPENDED to the
//     user-authored opts["system_prompt"], so the capability acts as a
//     workspace-wide pre-instruction. applySpecMemoryInjection still
//     appends spec/memory after the user prompt.
//
// First-wins is NOT applied: multiple system_prompt capabilities all
// contribute. Order is the enumeration order from
// GetEnabledCapabilitiesForAgent.
func mergeSystemPromptsIntoOptions(opts map[string]any, prompts []ResolvedSystemPrompt) {
	if len(prompts) == 0 {
		return
	}
	var appendParts, overrideParts []string
	for _, p := range prompts {
		content := strings.TrimSpace(p.Content)
		if content == "" {
			continue
		}
		switch p.Mode {
		case canonical.SystemPromptModeOverride:
			overrideParts = append(overrideParts, content)
		default:
			appendParts = append(appendParts, content)
		}
	}
	if len(overrideParts) > 0 {
		opts["override_system_prompt"] = strings.Join(overrideParts, "\n\n")
		// Override fully replaces the system prompt slot. Drop any
		// pre-existing system_prompt so downstream renderStaticAgentOptions
		// / spec/memory state doesn't sneak through.
		delete(opts, "system_prompt")
		return
	}
	if len(appendParts) == 0 {
		return
	}
	prefix := strings.Join(appendParts, "\n\n")
	base := stringFromMap(opts, "system_prompt")
	if base == "" {
		opts["system_prompt"] = prefix
		return
	}
	opts["system_prompt"] = prefix + "\n\n" + base
}

func resolveModelID(in connector.PromptInput) string {
	for _, cfg := range []map[string]any{in.ProjectAgentConfig, in.AgentConfig} {
		if v := stringFromMap(cfg, "model_id"); v != "" {
			return v
		}
	}
	for _, cfg := range []map[string]any{in.ProjectAgentConfig, in.AgentConfig} {
		if v := stringFromMap(cfg, "default_model_id"); v != "" {
			return v
		}
	}
	return ""
}

func isAnthropicRuntime(mr store.ModelRuntime) bool {
	for _, v := range []string{mr.ProviderType, mr.Adapter} {
		n := strings.ToLower(strings.TrimSpace(v))
		switch n {
		case "anthropic", "anthropic-compatible", "anthropic_compatible", "@ai-sdk/anthropic":
			return true
		}
	}
	return false
}

func anthropicCustomHeaders(providerConfig map[string]any) string {
	raw, _ := providerConfig["headers"].(map[string]any)
	if len(raw) == 0 {
		return ""
	}
	keys := make([]string, 0, len(raw))
	for k, v := range raw {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, strings.TrimSpace(k)+": "+strings.TrimSpace(raw[k].(string)))
	}
	return strings.Join(lines, "\n")
}

func copyStringAnyMap(v any) map[string]any {
	out := map[string]any{}
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			out[k] = val
		}
	}
	return out
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func mapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderStaticAgentOptions copies the agent-tunable keys the daemon's
// agent adapters know about into the prompt_request payload.
// Project-level overrides win, then workspace-level.
func renderStaticAgentOptions(in connector.PromptInput) map[string]any {
	opts := map[string]any{}
	for _, key := range []string{
		"model",
		"mode",
		"allowed_tools",
		"system_prompt",
		"override_system_prompt",
		"mcp_servers",
		"plugin_dirs",
		"env",
	} {
		if v, ok := in.AgentConfig[key]; ok {
			opts[key] = v
		}
		if v, ok := in.ProjectAgentConfig[key]; ok {
			opts[key] = v
		}
	}
	return opts
}

func newSecretService(masterKey string) (*secrets.Service, error) {
	if strings.TrimSpace(masterKey) == "" {
		return nil, nil
	}
	return secrets.New(masterKey)
}

// emitModelCredentialMissingNotice writes a credential-form notice for a
// credential_ref model whose caller has not yet bound the kind. Best-
// effort; failures only log.
func (c *Connector) emitModelCredentialMissingNotice(ctx context.Context, in connector.PromptInput, mr store.ModelRuntime) {
	if c.systemMessages == nil {
		c.log.Warn("agent_daemon: skip emitModelCredentialMissingNotice — SystemMessages not wired",
			"run_id", in.RunID,
			"model_id", mr.ModelID)
		return
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return
	}
	kind := strings.TrimSpace(mr.CredentialKindCode)
	if kind == "" {
		c.log.Warn("agent_daemon: skip emitModelCredentialMissingNotice — model has no credential_kind_code",
			"run_id", in.RunID,
			"model_id", mr.ModelID)
		return
	}
	if _, err := c.systemMessages.CreateRuntimeErrorSystemMessage(ctx, store.CreateRuntimeErrorSystemMessageInput{
		WorkspaceID:    in.WorkspaceID,
		ProjectID:      in.ProjectID,
		AgentID:        in.AgentID,
		RunID:          in.RunID,
		ConversationID: in.ConversationID,
		SubKind:        CapabilityCredentialMissing,
		CapabilityID:   ModelCredentialMissingCapabilityID,
		CapabilityName: buildModelCredentialCapabilityName(mr),
		CredentialKind: kind,
	}); err != nil {
		c.log.Warn("agent_daemon: emit model-credential-missing system message failed",
			"run_id", in.RunID,
			"model_id", mr.ModelID,
			"kind", kind,
			"err", err)
	}
}

func buildModelCredentialCapabilityName(mr store.ModelRuntime) string {
	provider := strings.TrimSpace(mr.ProviderType)
	modelKey := strings.TrimSpace(mr.ModelKey)
	switch {
	case provider != "" && modelKey != "":
		return "模型 · " + provider + "/" + modelKey
	case modelKey != "":
		return "模型 · " + modelKey
	case strings.TrimSpace(mr.ModelName) != "":
		return "模型 · " + strings.TrimSpace(mr.ModelName)
	default:
		return "模型"
	}
}
