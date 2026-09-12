package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type configuration struct {
	Agent       v1.Agent       `json:"agent"`
	Environment v1.Environment `json:"environment"`
}

func resolve(input v1.CreateSessionRequest, tenant, key string) (json.RawMessage, error) {
	if input.AgentID != nil || input.Stream || len(input.VaultIDs) > 0 || (len(input.Input) > 0 && !bytes.Equal(bytes.TrimSpace(input.Input), []byte("null"))) {
		return nil, errors.New("Saved agents, initial input, streaming and vaults are not supported by this service yet.")
	}
	if input.Agent == nil || strings.TrimSpace(input.Agent.Model) == "" {
		return nil, errors.New("agent.model is required for an inline agent.")
	}
	if input.Environment == nil || input.Environment.Type != "none" {
		return nil, errors.New("This service currently requires environment.type=none.")
	}
	if len(input.Metadata) > 16 {
		return nil, errors.New("metadata supports at most 16 pairs.")
	}
	for k, value := range input.Metadata {
		if utf8.RuneCountInString(k) > 64 || utf8.RuneCountInString(value) > 512 {
			return nil, errors.New("metadata keys must be at most 64 characters and values at most 512 characters.")
		}
	}
	text, err := resolveText(input.Agent.Text)
	if err != nil {
		return nil, err
	}
	// Inline execution configuration has its own stable identity for creation retries.
	id := "agent_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant+"\x00"+key)).String()
	return json.Marshal(configuration{Agent: v1.Agent{
		ID: id, Model: input.Agent.Model, Instructions: input.Agent.Instructions,
		ServiceTier: "auto", Text: text,
		Tools: []json.RawMessage{},
	}, Environment: *input.Environment})
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
		RequiredActions: []json.RawMessage{}, VaultIDs: []string{},
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
		case store.TurnFailed:
			response.Status = "failed"
			message := "The execution could not complete."
			response.Error = &message
		}
	}
	return response, nil
}
