package agentdaemon

import (
	"encoding/json"
	"fmt"
	"strings"

	runtimeopencode "github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/opencode"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func injectMCodeManagedModel(opts map[string]any, modelID string, mr store.ModelRuntime, apiKey string) error {
	mr.BaseURL = openCodeEndpointBaseURL(mr)
	data, err := runtimeopencode.RenderConfig(mr, apiKey, runtimeopencode.RenderInput{})
	if err != nil {
		return fmt.Errorf("agent_daemon: render mcode model %s: %w", modelID, err)
	}
	if len(data) == 0 {
		return fmt.Errorf("%w: mcode model_id=%s", ErrManagedModelConfigInvalid, modelID)
	}
	var config struct {
		Providers map[string]map[string]any `json:"provider"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("agent_daemon: decode mcode provider: %w", err)
	}
	provider := config.Providers[mr.ProviderType]
	if len(provider) == 0 {
		return fmt.Errorf("%w: mcode provider is missing", ErrManagedModelConfigInvalid)
	}
	provider["kind"], provider["enabled"] = "custom", true
	delete(provider, "whitelist")
	opts["mcode_provider"] = provider
	opts["model"] = strings.TrimSpace(mr.ModelKey)
	return nil
}
