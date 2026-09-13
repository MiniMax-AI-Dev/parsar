package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func initialSessionInputs(raw json.RawMessage) ([]store.Input, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, store.ErrInvalidInput
		}
		raw, _ = json.Marshal([]v1.InputMessage{{Role: "user", Content: []v1.InputContent{{Type: "input_text", Text: text}}}})
	}
	// Preserve the array's original fields for the shared strict message decoder.
	event, err := json.Marshal(struct {
		Type  string          `json:"type"`
		Input json.RawMessage `json:"input"`
	}{Type: "agent.session.input.message", Input: raw})
	if err != nil {
		return nil, store.ErrInvalidInput
	}
	return executionInputs([]json.RawMessage{event})
}
