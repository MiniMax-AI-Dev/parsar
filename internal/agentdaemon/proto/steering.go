package proto

// TypePromptSteer appends text to an active run; Envelope.ID is the run ID.
const TypePromptSteer = "prompt_steer"

// TypePromptSteerAck reports engine acceptance on the originating run ID.
const TypePromptSteerAck = "prompt_steer_ack"

// PromptSteerPayload identifies one text input within an active run.
type PromptSteerPayload struct {
	InputID string `json:"input_id"`
	Text    string `json:"text"`
}

// PromptSteerAckPayload confirms acceptance, not completion of the input.
type PromptSteerAckPayload struct {
	InputID   string `json:"input_id"`
	Accepted  bool   `json:"accepted"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}
