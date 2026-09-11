package v1

// SessionInput contains the supported message and cancellation event variants.
type SessionInput struct {
	Type  string         `json:"type" enums:"agent.session.input.message,agent.session.input.cancel" binding:"required"`
	Input []InputMessage `json:"input,omitempty"`
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
