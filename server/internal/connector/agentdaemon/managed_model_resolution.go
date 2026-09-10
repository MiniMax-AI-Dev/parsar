package agentdaemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// injectManagedModel writes the Parsar-managed model selection into the
// daemon prompt_request. claude_code receives ANTHROPIC_* env + model flag;
// opencode receives a rendered opencode.json blob plus the model selector.
func (c *Connector) injectManagedModel(ctx context.Context, in connector.PromptInput, opts map[string]any, agentKind string) (string, error) {
	agentKind = strings.TrimSpace(agentKind)
	modelID := resolveModelID(in)
	if modelID == "" {
		c.log.Warn("agent_daemon: injectManagedModel skipped — no model_id or default_model_id found",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"agent_config_keys", mapKeys(in.AgentConfig))
		return "", nil
	}
	c.log.Info("agent_daemon: injectManagedModel resolving",
		"run_id", in.RunID,
		"agent_kind", agentKind,
		"model_id", modelID,
		"has_model_resolver", c.modelResolver != nil,
		"has_secrets", c.secrets != nil)
	if c.modelResolver == nil {
		return "", ErrManagedModelResolverMissing
	}
	if c.secrets == nil {
		return "", ErrManagedModelSecretsMissing
	}

	// Decision table:
	//   has shared model_credential_binding → ResolveModelRuntime (metadata
	//                                         only); the workspace secret is
	//                                         consumed below.
	//   otherwise                           → ResolveModelRuntimeForUser
	//                                         (per-user for credential_ref;
	//                                         a no-op for inline_secret).
	// store.ResolveModelRuntimeForUser still rejects credential_ref + empty
	// initiator — that surfaces the "missing credentials" notice via the err branch below.
	// Mirrors capability_runtime.resolveCredentialValues: binding wins over
	// initiator presence, never the other way.
	modelBinding, hasModelBinding := ParseModelCredentialBinding(in.AgentConfig)
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
			return "", fmt.Errorf("%w: model_id=%s user_id=%s: %v",
				ErrManagedModelPersonalCredMissing, modelID, in.ConversationInitiatorID, err)
		}
		return "", fmt.Errorf("agent_daemon: resolve model %s: %w", modelID, err)
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
			return "", fmt.Errorf("agent_daemon: load shared model secret model_id=%s secret_id=%s: %w", modelID, modelBinding.SecretID, err)
		}
		if secret.Status != "active" {
			return "", fmt.Errorf("%w: secret_id=%s status=%s", ErrManagedModelSecretMissing, modelBinding.SecretID, secret.Status)
		}
		payload, err := c.secrets.Decrypt(secret.EncryptedPayload)
		if err != nil {
			return "", fmt.Errorf("agent_daemon: decrypt shared model secret model_id=%s secret_id=%s: %w", modelID, modelBinding.SecretID, err)
		}
		if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	case mr.CredentialMode == "credential_ref":
		if strings.TrimSpace(in.ConversationInitiatorID) == "" {
			return "", fmt.Errorf("%w: model_id=%s", ErrManagedModelUserIDMissing, modelID)
		}
		if len(mr.EncryptedPayload) == 0 {
			c.emitModelCredentialMissingNotice(ctx, in, mr)
			return "", fmt.Errorf("%w: model_id=%s user_id=%s",
				ErrManagedModelPersonalCredMissing, modelID, in.ConversationInitiatorID)
		}
		payload, err := c.secrets.Decrypt(mr.EncryptedPayload)
		if err != nil {
			return "", fmt.Errorf("agent_daemon: decrypt user credential for model %s: %w", modelID, err)
		}
		if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	default:
		if strings.TrimSpace(mr.SecretID) == "" {
			return "", fmt.Errorf("%w: model_id=%s", ErrManagedModelSecretMissing, modelID)
		}
		secret, err := c.modelResolver.GetSecretPayload(ctx, in.WorkspaceID, mr.SecretID)
		if err != nil {
			return "", fmt.Errorf("agent_daemon: load model secret %s: %w", mr.SecretID, err)
		}
		if secret.Status != "active" {
			return "", fmt.Errorf("%w: secret_id=%s status=%s", ErrManagedModelSecretMissing, mr.SecretID, secret.Status)
		}
		payload, err := c.secrets.Decrypt(secret.EncryptedPayload)
		if err != nil {
			return "", fmt.Errorf("agent_daemon: decrypt model secret %s: %w", mr.SecretID, err)
		}
		if v, _ := payload["api_key"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		} else if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("%w: model_id=%s payload missing api_key/value", ErrManagedModelSecretMissing, modelID)
	}

	switch agentKind {
	case "claude_code":
		if err := injectClaudeManagedModel(opts, modelID, mr, apiKey); err != nil {
			return "", err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"has_base_url", strings.TrimSpace(mr.BaseURL) != "",
			"env_key_count", len(copyStringAnyMap(opts["env"])))
		return strings.TrimSpace(mr.ProviderType), nil
	case "opencode":
		if err := injectOpenCodeManagedModel(opts, modelID, mr, apiKey); err != nil {
			return "", err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"has_opencode_json", stringFromMap(opts, "opencode_json") != "")
		return strings.TrimSpace(mr.ProviderType), nil
	case "codex":
		if err := injectCodexManagedModel(opts, modelID, mr, apiKey); err != nil {
			return "", err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"env_key_count", len(copyStringAnyMap(opts["env"])))
		return strings.TrimSpace(mr.ProviderType), nil
	case "pi":
		if err := injectPiManagedModel(opts, modelID, mr, apiKey); err != nil {
			return "", err
		}
		c.log.Info("agent_daemon: injectManagedModel ok",
			"run_id", in.RunID,
			"agent_kind", agentKind,
			"model_id", modelID,
			"model_key", mr.ModelKey,
			"provider_slug", mr.ProviderType,
			"pi_model", stringFromMap(opts, "model"))
		return strings.TrimSpace(mr.ProviderType), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAgentKind, agentKind)
	}
}
