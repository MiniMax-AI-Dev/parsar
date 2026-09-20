package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
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
	if len(caps) != 0 {
		return openai.BetaAgentSessionNewParams{}, errors.New("this Core product integration does not support capability bindings yet")
	}
	config := make(map[string]any, len(in.AgentConfig))
	for _, key := range []string{"model", "tools", "service_tier", "multi_agent", "reasoning", "text"} {
		if value, ok := in.AgentConfig[key]; ok {
			config[key] = value
		}
	}
	config["instructions"], _ = in.AgentConfig["system_prompt"].(string)
	raw, err := json.Marshal(config)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, err
	}
	var agent openai.BetaAgentSessionNewParamsAgent
	if err := json.Unmarshal(raw, &agent); err != nil {
		return openai.BetaAgentSessionNewParams{}, errors.New("invalid Core Agent configuration")
	}
	agent.SetExtraFields(config) // Preserve null, omission and exact nested protocol payloads.
	conversation, err := c.store.GetConversation(ctx, in.ConversationID)
	if err != nil {
		return openai.BetaAgentSessionNewParams{}, errPersistence
	}
	environment := openai.EnvironmentParamUnion{OfParamOpenAIHosted: &openai.EnvironmentParamOpenAIHosted{}}
	if selection, ok := conversation.Metadata["core_environment"]; ok {
		environment = openai.EnvironmentParamUnion{}
		raw, err := json.Marshal(selection)
		if err != nil {
			return openai.BetaAgentSessionNewParams{}, err
		}
		if err := json.Unmarshal(raw, &environment); err != nil {
			return openai.BetaAgentSessionNewParams{}, errors.New("invalid Core environment selection")
		}
	}
	return openai.BetaAgentSessionNewParams{
		Agent:       agent,
		Environment: environment,
		Metadata:    map[string]string{"parsar_workspace_id": in.WorkspaceID, "parsar_conversation_id": in.ConversationID, "parsar_agent_id": in.AgentID},
	}, nil
}
