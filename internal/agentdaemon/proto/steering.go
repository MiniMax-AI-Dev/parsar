package proto

// TypePromptSteer appends text to an active run; Envelope.ID is the run ID.
const TypePromptSteer = "prompt_steer"

// TypePromptSteerAck reports input receipt phases on the originating run ID.
const TypePromptSteerAck = "prompt_steer_ack"

// PromptSteerPayload identifies one text input within an active run.
type PromptSteerPayload struct {
	InputID        string `json:"input_id"`
	Text           string `json:"text"`
	DurableReceipt bool   `json:"durable_receipt,omitempty"`
}

// PromptSteerAckPayload distinguishes a completed write from native acceptance.
type PromptSteerAckPayload struct {
	InputID  string `json:"input_id"`
	Accepted bool   `json:"accepted"`
	// Written is an intermediate transport phase, never native acceptance.
	Written   bool   `json:"written,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}
