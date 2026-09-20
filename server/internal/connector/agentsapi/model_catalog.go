package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/openai/openai-go/v3/option"
)

type resourceSessionStore interface {
	EnsureCoreSessionWithResources(context.Context, string, json.RawMessage, string, map[string]any, string) (store.CoreSessionBinding, error)
	CoreSessionPrivateEnvironment(store.CoreSessionBinding) (map[string]any, error)
}

type modelSessionStore interface {
	EnsureCoreSessionWithModel(context.Context, string, json.RawMessage, string) (store.CoreSessionBinding, error)
	CoreSessionProvider(store.CoreSessionBinding) (*v1.SessionExecutionInput, error)
}

func (c *Connector) ensureModelSession(ctx context.Context, in connector.PromptInput, raw json.RawMessage) (store.CoreSessionBinding, error) {
	modelID, _ := in.AgentConfig["model_id"].(string)
	if resources, ok := c.store.(resourceSessionStore); ok {
		binding, _ := in.AgentConfig["model_credential_binding"].(map[string]any)
		return resources.EnsureCoreSessionWithResources(ctx, in.RunID, raw, modelID, binding, in.ConversationInitiatorID)
	}
	if catalog, ok := c.store.(modelSessionStore); ok {
		return catalog.EnsureCoreSessionWithModel(ctx, in.RunID, raw, modelID)
	}
	if modelID != "" {
		return store.CoreSessionBinding{}, errors.New("Core model catalog is unavailable")
	}
	return c.store.EnsureCoreSession(ctx, in.RunID, raw)
}
func (c *Connector) modelSessionOptions(binding store.CoreSessionBinding) ([]option.RequestOption, error) {
	if len(binding.ProviderSnapshot) == 0 {
		return nil, nil
	}
	catalog, ok := c.store.(modelSessionStore)
	if !ok {
		return nil, errors.New("Core model catalog is unavailable")
	}
	extension, err := catalog.CoreSessionProvider(binding)
	if err != nil {
		return nil, err
	}
	options := []option.RequestOption{option.WithJSONSet("x_agents_core", extension)}
	if resources, ok := c.store.(resourceSessionStore); ok {
		environment, err := resources.CoreSessionPrivateEnvironment(binding)
		if err != nil {
			return nil, err
		}
		for key, value := range environment {
			options = append(options, option.WithJSONSet("environment."+key, value))
		}
	}
	return options, nil
}
