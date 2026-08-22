package agentdaemon

import (
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// deepseekHarnessAPIKeyEnv is the env var the daemon sets to the decrypted
// secret and that the materialised dsh patch overlay references through the
// route's apiKeyEnv field. Carrying the key by env-var name keeps it off the
// dsh child's argv, where `ps` would leak it.
const deepseekHarnessAPIKeyEnv = "PARSAR_DSH_API_KEY"

// injectDeepseekHarnessManagedModel stamps the Parsar-managed model into an
// agent_kind="deepseek_harness" prompt_request.
//
// DeepSeek Harness resolves its model through the `agent-default-model` row,
// which must name a live llm route. Its shipped DeepSeek route hard-codes the
// upstream endpoint, so a Parsar gateway model has to arrive as a declared
// route on the harness's generic `llm-pi-ai` adapter instead. That adapter is
// backed by the same @earendil-works/pi-ai library the pi CLI uses, so the
// wire-protocol mapping is shared with injectPiManagedModel.
//
// What lands in agent_options:
//
//	dsh_provider:
//	  base_url    — mr.BaseURL (required; a declared route has no default)
//	  api         — pi-ai wire protocol (anthropic-messages /
//	                openai-completions / google-generative-ai)
//	  api_key_env — deepseekHarnessAPIKeyEnv, referenced by the route
//	  model       — mr.ModelKey, the route's single catalog entry
//	  name        — mr.ModelName (display, optional)
//	  headers     — flattened mr.ProviderConfig.headers (e.g. X-Sub-Module)
//	model            — mr.ModelKey, pinned on agent-default-model
//	env[deepseekHarnessAPIKeyEnv] — the decrypted secret
//
// All guards run before any opts mutation so a rejection leaves opts clean.
func injectDeepseekHarnessManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	api := piAPIProtocol(mr)
	if api == "" {
		return fmt.Errorf("%w: model_id=%s provider_type=%q adapter=%q",
			ErrManagedModelUnsupported, modelID, mr.ProviderType, mr.Adapter)
	}
	modelKey := strings.TrimSpace(mr.ModelKey)
	if modelKey == "" {
		return fmt.Errorf("%w: model_id=%s deepseek_harness requires a model_key",
			ErrManagedModelConfigInvalid, modelID)
	}
	baseURL := modelEndpointBaseURL(mr, piAPIEndpointType(mr))
	if baseURL == "" {
		return fmt.Errorf("%w: model_id=%s base_url is required for deepseek_harness provider injection",
			ErrManagedModelConfigInvalid, modelID)
	}

	provider := map[string]any{
		"base_url":    baseURL,
		"api":         api,
		"api_key_env": deepseekHarnessAPIKeyEnv,
		"model":       modelKey,
	}
	if name := strings.TrimSpace(mr.ModelName); name != "" {
		provider["name"] = name
	}
	if headers := flattenStringMap(mr.ProviderConfig, "headers"); len(headers) > 0 {
		provider["headers"] = headers
	}

	opts["model"] = modelKey
	opts["dsh_provider"] = provider

	env := copyStringAnyMap(opts["env"])
	env[deepseekHarnessAPIKeyEnv] = apiKey
	opts["env"] = env
	return nil
}
