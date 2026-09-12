package proto

import (
	"encoding/json"
	"errors"
)

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
	DeliveryID string                  `json:"delivery_id"`
	CallID     string                  `json:"call_id"`
	Success    bool                    `json:"success"`
	Content    []FunctionResultContent `json:"content"`
}

// FunctionResultContent is one ordered text or image part of a function result.
type FunctionResultContent struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

func (r FunctionResultPayload) ValidateContent() error {
	if r.Content == nil {
		return errors.New("function result requires a content array")
	}
	for _, part := range r.Content {
		switch part.Type {
		case "input_text":
			if part.Text != nil && part.ImageURL == nil {
				continue
			}
		case "input_image":
			if part.ImageURL != nil && part.Text == nil {
				continue
			}
		}
		return errors.New("function result requires text or image content")
	}
	return nil
}
