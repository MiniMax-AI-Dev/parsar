package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type configuration struct {
	Agent       v1.Agent       `json:"agent"`
	Environment v1.Environment `json:"environment"`
}

func resolve(input sessionRequest, tenant, key string, saved *v1.SavedAgent) (json.RawMessage, error) {
	if input.Stream || len(input.VaultIDs) > 0 {
		return nil, errors.New("Streaming creation and vaults are not supported by this service yet.")
	}
	if input.Environment == nil || input.Environment.Type != "none" {
		return nil, errors.New("This service currently requires environment.type=none.")
	}
	if err := validateMetadata(input.Metadata); err != nil {
		return nil, err
	}
	agent, err := resolveSessionAgent(input, saved)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		// Inline execution configuration has its own stable identity for creation retries.
		agent.ID = "agent_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant+"\x00"+key)).String()
	} else {
		agent.ID = saved.ID
	}
	return json.Marshal(configuration{Agent: agent, Environment: *input.Environment})
}

func sessionResponse(session store.Session) (v1.Session, error) {
	var cfg configuration
	if err := json.Unmarshal(session.Configuration, &cfg); err != nil || cfg.Agent.ID == "" || cfg.Agent.Model == "" || cfg.Environment.Type != "none" {
		return v1.Session{}, errors.New("unsupported stored session configuration")
	}
	response := v1.Session{
		ID: session.ID, Agent: cfg.Agent, Environment: cfg.Environment, Usage: tokenUsage(session.Usage),
		CreatedAt: session.CreatedAt.Unix(), LastActiveAt: session.CreatedAt.Unix(),
		Metadata: session.Metadata, Object: "agent.session", Status: "idle",
		RequiredActions: []v1.FunctionCallAction{}, VaultIDs: []string{},
	}
	if turn := session.LastTurn; turn != nil {
		active := turn.CreatedAt
		if turn.StartedAt.After(active) {
			active = turn.StartedAt
		}
		if turn.CompletedAt.After(active) {
			active = turn.CompletedAt
		}
		response.LastActiveAt = active.Unix()
		switch turn.Status {
		case store.TurnQueued, store.TurnInProgress, store.TurnWaiting:
			response.Status = "in_progress"
			if turn.CancelRequestedAt.IsZero() && len(session.RequiredActions) > 0 {
				response.Status = "requires_action"
				response.RequiredActions = session.RequiredActions
			}
		case store.TurnFailed:
			response.Status = "failed"
			message := "The execution could not complete."
			response.Error = &message
		}
	}
	return response, nil
}
