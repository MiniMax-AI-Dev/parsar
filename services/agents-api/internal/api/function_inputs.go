package api

import (
	"bytes"
	"encoding/json"
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type decodedInputEvent struct {
	v1.SessionInput
	Output json.RawMessage `json:"output,omitempty"`
}

func decodeInputEvent(raw json.RawMessage) (decodedInputEvent, error) {
	var event decodedInputEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, store.ErrInvalidInput
	}
	fields := []string{"type"}
	switch event.Type {
	case "agent.session.input.message":
		fields = append(fields, "input")
	case "agent.session.input.cancel":
	case "agent.session.input.tool_result":
		fields = append(fields, "call_id", "turn_id", "success", "error", "output")
	default:
		return event, store.ErrInvalidInput
	}
	return event, decodeInputObject(raw, &event, fields...)
}

func decodeInputObject(raw json.RawMessage, value any, allowed ...string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return store.ErrInvalidInput
	}
	for field := range fields {
		if !slices.Contains(allowed, field) {
			return store.ErrInvalidInput
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return store.ErrInvalidInput
	}
	return nil
}

func functionResultInput(event decodedInputEvent) (store.Input, error) {
	if event.CallID == "" || event.TurnID == "" || event.Success == nil {
		return store.Input{}, store.ErrInvalidInput
	}
	if len(event.Error) > 0 && !bytes.Equal(bytes.TrimSpace(event.Error), []byte("null")) {
		var message string
		if json.Unmarshal(event.Error, &message) != nil {
			return store.Input{}, store.ErrInvalidInput
		}
	}
	if err := validateFunctionOutput(event.Output); err != nil {
		return store.Input{}, err
	}
	result, err := json.Marshal(struct {
		Success bool            `json:"success"`
		Error   json.RawMessage `json:"error,omitempty"`
		Output  json.RawMessage `json:"output,omitempty"`
	}{*event.Success, event.Error, event.Output})
	if err != nil {
		return store.Input{}, err
	}
	payload, err := json.Marshal(store.FunctionResultInput{TurnID: event.TurnID, CallID: event.CallID, Result: result})
	return store.Input{Kind: "tool_result", Payload: payload}, err
}

func validateFunctionOutput(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return store.ErrInvalidInput
	}
	for _, part := range parts {
		var value struct {
			Type     string  `json:"type"`
			Text     *string `json:"text"`
			ImageURL *string `json:"image_url"`
		}
		if json.Unmarshal(part, &value) != nil {
			return store.ErrInvalidInput
		}
		field := "text"
		switch value.Type {
		case "input_text":
			if value.Text == nil {
				return store.ErrInvalidInput
			}
		case "input_image":
			field = "image_url"
			if value.ImageURL == nil {
				return store.ErrInvalidInput
			}
		default:
			return store.ErrInvalidInput
		}
		if err := decodeInputObject(part, &value, "type", field); err != nil {
			return err
		}
	}
	return nil
}
