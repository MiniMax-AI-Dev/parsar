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
	AgentID  json.RawMessage    `json:"agent_id"`
	Stream   json.RawMessage    `json:"stream"`
	Metadata map[string]*string `json:"metadata"`
}

func (request decodedSessionRequest) validated() (v1.CreateSessionRequest, error) {
	input := request.CreateSessionRequest
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
	if request.Metadata != nil {
		input.Metadata = make(map[string]string, len(request.Metadata))
		for key, value := range request.Metadata {
			if value == nil {
				return input, store.ErrInvalidInput
			}
			input.Metadata[key] = *value
		}
	}
	return input, nil
}
