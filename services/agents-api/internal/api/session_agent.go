package api

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// Resolve wire defaults before admitting the effective execution configuration.
// A saved resource remains usable with other executors even when this one cannot
// execute its options. Overrides replace whole fields; they never mutate it.
func resolveSessionAgent(input sessionRequest, saved *v1.SavedAgent) (v1.Agent, error) {
	request := v1.CreateAgentRequest{}
	if agent := input.Agent; agent != nil {
		request.XAgentsCore = agent.XAgentsCore
		request.Model, request.Instructions = agent.Model, agent.Instructions
		request.MultiAgent, request.Reasoning, request.ServiceTier = agent.MultiAgent, agent.Reasoning, agent.ServiceTier
		request.Text, request.Tools = agent.Text, agent.Tools
	}
	if saved != nil && request.Model == nil {
		request.Model = &saved.Model
	}
	if request.Model == nil {
		return v1.Agent{}, errors.New("agent.model is required without agent_id.")
	}
	resolved, err := resolveSavedAgent(request)
	if err != nil {
		return v1.Agent{}, err
	}
	var override v1.SavedAgentConfiguration
	if err := json.Unmarshal(resolved.Configuration, &override); err != nil {
		return v1.Agent{}, err
	}
	cfg := override
	if saved != nil {
		cfg = saved.SavedAgentConfiguration
		for field := range input.agentFields {
			switch field {
			case "x_agents_core":
				cfg.XAgentsCore = override.XAgentsCore
			case "model":
				cfg.Model = override.Model
			case "instructions":
				cfg.Instructions = override.Instructions
			case "multi_agent":
				cfg.MultiAgent = override.MultiAgent
			case "reasoning":
				cfg.Reasoning = override.Reasoning
			case "service_tier":
				cfg.ServiceTier = override.ServiceTier
			case "text":
				cfg.Text = override.Text
			case "tools":
				cfg.Tools = override.Tools
			}
		}
	}
	return admitSessionAgent(cfg)
}

func admitSessionAgent(cfg v1.SavedAgentConfiguration) (v1.Agent, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return v1.Agent{}, errors.New("Execution currently requires a nonempty model.")
	}
	if cfg.MultiAgent.Enabled || cfg.MultiAgent.MaxConcurrentSubagents != nil {
		return v1.Agent{}, errors.New("Enabled multi_agent execution is not supported by this service yet.")
	}
	if cfg.Reasoning.Effort != nil || cfg.Reasoning.Summary != nil {
		return v1.Agent{}, errors.New("Explicit reasoning execution options are not supported by this service yet.")
	}
	if cfg.ServiceTier != "auto" {
		return v1.Agent{}, errors.New("Execution currently supports service_tier=auto only.")
	}
	if cfg.Text.Format.Type != "text" || len(cfg.Text.Format.Schema) > 0 {
		return v1.Agent{}, errors.New("Execution currently supports text.format.type=text only.")
	}
	text, err := resolveText(&v1.TextConfigInput{Verbosity: &cfg.Text.Verbosity})
	if err != nil {
		return v1.Agent{}, err
	}
	tools, err := resolveSessionTools(cfg.Tools)
	if err != nil {
		return v1.Agent{}, err
	}
	return v1.Agent{XAgentsCore: cfg.XAgentsCore, Model: cfg.Model, Name: cfg.Name, Instructions: cfg.Instructions,
		MultiAgent: cfg.MultiAgent, Reasoning: cfg.Reasoning, ServiceTier: cfg.ServiceTier,
		Text: text, Tools: tools}, nil
}
