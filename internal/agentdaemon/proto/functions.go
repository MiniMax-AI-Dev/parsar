package proto

import "encoding/json"

const (
	TypeFunctionCall   = "function_call"
	TypeFunctionResult = "function_result"
)

type FunctionTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// FunctionCallPayload belongs to the Run identified by Envelope.ID.
type FunctionCallPayload struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type FunctionResultPayload struct {
	DeliveryID string `json:"delivery_id"`
	CallID     string `json:"call_id"`
	Success    bool   `json:"success"`
	Text       string `json:"text"`
}
