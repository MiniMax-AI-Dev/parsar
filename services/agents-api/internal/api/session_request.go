package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Keep explicit null until validation for fields whose Go zero values would
// otherwise erase it. The embedded wire type retains strict nested decoding.
type decodedSessionRequest struct {
	v1.CreateSessionRequest
	Input       json.RawMessage    `json:"input"`
	Agent       json.RawMessage    `json:"agent"`
	AgentID     json.RawMessage    `json:"agent_id"`
	Environment json.RawMessage    `json:"environment"`
	Stream      json.RawMessage    `json:"stream"`
	Metadata    map[string]*string `json:"metadata"`
}

type sessionRequest struct {
	v1.CreateSessionRequest
	Input       json.RawMessage
	agentFields map[string]json.RawMessage
}

func (request decodedSessionRequest) validated() (sessionRequest, error) {
	input := sessionRequest{CreateSessionRequest: request.CreateSessionRequest, Input: request.Input}
	var err error
	input.Environment, err = decodeSessionEnvironment(request.Environment)
	if err != nil {
		return input, err
	}
	if len(request.Agent) > 0 {
		if decodeInputObject(request.Agent, &input.Agent, "model", "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools") != nil {
			return input, store.ErrInvalidInput
		}
		if err := json.Unmarshal(request.Agent, &input.agentFields); err != nil {
			return input, store.ErrInvalidInput
		}
		if _, supplied := input.agentFields["model"]; supplied && input.Agent.Model == nil {
			return input, store.ErrInvalidInput
		}
	}
	if len(request.AgentID) > 0 {
		var id string
		if bytes.Equal(bytes.TrimSpace(request.AgentID), []byte("null")) || json.Unmarshal(request.AgentID, &id) != nil {
			return input, store.ErrInvalidInput
		}
		input.AgentID = &id
	}
	if len(request.Stream) > 0 {
		if bytes.Equal(bytes.TrimSpace(request.Stream), []byte("null")) || json.Unmarshal(request.Stream, &input.Stream) != nil {
			return input, store.ErrInvalidInput
		}
	}
	input.Metadata, err = stringMetadata(request.Metadata)
	return input, err
}
