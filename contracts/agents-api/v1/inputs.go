package v1

import "encoding/json"

// SessionInput contains the message, cancellation and function-result event variants.
type SessionInput struct {
	Type    string          `json:"type" enums:"agent.session.input.message,agent.session.input.cancel,agent.session.input.tool_result" binding:"required"`
	Input   []InputMessage  `json:"input,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	TurnID  string          `json:"turn_id,omitempty"`
	Success *bool           `json:"success,omitempty"`
	Error   json.RawMessage `json:"error,omitempty" swaggertype:"string" extensions:"x-nullable"`
	Output  any             `json:"output,omitempty" extensions:"x-nullable"`
}

type InputMessage struct {
	Type    string         `json:"type,omitempty" enums:"message"`
	Role    string         `json:"role" enums:"user" binding:"required"`
	Content []InputContent `json:"content" binding:"required"`
}

type InputContent struct {
	Type string `json:"type" enums:"input_text" binding:"required"`
	Text string `json:"text" binding:"required"`
}

type CreateEventsRequest struct {
	Events []SessionInput `json:"events" binding:"required"`
}
