package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/openai/openai-go/v3"
)

func (c *Connector) sessionRequest(ctx context.Context, in connector.PromptInput) (openai.BetaAgentSessionNewParams, error) {
	model, _ := in.AgentConfig["model"].(string)
	if strings.TrimSpace(model) == "" {
		return openai.BetaAgentSessionNewParams{}, errors.New("select a Core model before starting a conversation")
	}
	caps, err := c.store.GetEnabledCapabilitiesForAgent(ctx, in.AgentID)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, errPersistence
	}
	config := make(map[string]any, len(in.AgentConfig))
	for _, key := range []string{"model", "tools", "service_tier", "multi_agent", "reasoning", "text", "x_agents_core"} {
		if value, ok := in.AgentConfig[key]; ok {
			config[key] = value
		}
	}
	config["instructions"], _ = in.AgentConfig["system_prompt"].(string)
	conversation, err := c.store.GetConversation(ctx, in.ConversationID)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, errPersistence
	}
	environment := openai.EnvironmentParamUnion{OfParamOpenAIHosted: &openai.EnvironmentParamOpenAIHosted{}}
	selection, ok := conversation.Metadata["core_environment"]
	if !ok {
		selection, ok = in.AgentConfig["environment"]
	}
	if ok {
		if _, err := store.ParseCoreEnvironment(selection); err != nil {
			return openai.BetaAgentSessionNewParams{}, err
		}
		environment = openai.EnvironmentParamUnion{}
		raw, err := json.Marshal(selection)
		if err != nil {
			return openai.BetaAgentSessionNewParams{}, err
		}
		if err := json.Unmarshal(raw, &environment); err != nil {
			return openai.BetaAgentSessionNewParams{}, errors.New("invalid Core environment selection")
		}
	}
	rawEnvironment, err := json.Marshal(environment)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	var environmentFields map[string]any
	if json.Unmarshal(rawEnvironment, &environmentFields) != nil {
		return openai.BetaAgentSessionNewParams{}, errors.New("invalid environment")
	}
	if err := c.projectResources(ctx, caps, config, environmentFields); err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	rawEnvironment, err = json.Marshal(environmentFields)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	if err := json.Unmarshal(rawEnvironment, &environment); err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	var agent openai.BetaAgentSessionNewParamsAgent
	if err := json.Unmarshal(raw, &agent); err != nil {
		return openai.BetaAgentSessionNewParams{}, errors.New("invalid Core Agent configuration")
	}
	agent.SetExtraFields(config) // Preserve null, omission and exact nested protocol payloads.
	return openai.BetaAgentSessionNewParams{
		Agent:       agent,
		Environment: environment,
		Metadata:    map[string]string{"parsar_workspace_id": in.WorkspaceID, "parsar_conversation_id": in.ConversationID, "parsar_agent_id": in.AgentID},
	}, nil
}
