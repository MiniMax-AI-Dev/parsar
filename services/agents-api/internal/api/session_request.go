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
	VaultIDs    json.RawMessage    `json:"vault_ids"`
}

type sessionRequest struct {
	initialFiles        []store.InitialFile
	originalEnvironment json.RawMessage
	v1.CreateSessionRequest
	Input               json.RawMessage
	templateID          string
	templateEnvironment json.RawMessage
	agentFields         map[string]json.RawMessage
}

func (request decodedSessionRequest) validated() (sessionRequest, error) {
	input := sessionRequest{CreateSessionRequest: request.CreateSessionRequest, Input: request.Input}
	var vaultIDs []*string
	if len(request.VaultIDs) != 0 && json.Unmarshal(request.VaultIDs, &vaultIDs) != nil {
		return input, store.ErrInvalidInput
	}
	input.VaultIDs = make([]string, 0, len(vaultIDs))
	for _, id := range vaultIDs {
		if id == nil {
			return input, store.ErrInvalidInput
		}
		input.VaultIDs = append(input.VaultIDs, *id)
	}
	input.originalEnvironment = request.Environment
	var environmentFields map[string]json.RawMessage
	if json.Unmarshal(request.Environment, &environmentFields) != nil {
		return input, store.ErrInvalidInput
	}
	var err error
	input.initialFiles, err = decodeInitialFiles(environmentFields["files"])
	if err != nil {
		return input, err
	}
	input.Environment, input.templateID, input.templateEnvironment, err = decodeTemplateEnvironment(request.Environment)
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
