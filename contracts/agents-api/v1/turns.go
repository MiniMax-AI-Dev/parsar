package v1

type Turn struct {
	ID          string      `json:"id" binding:"required"`
	AgentID     string      `json:"agent_id" binding:"required"`
	SessionID   string      `json:"session_id" binding:"required"`
	Object      string      `json:"object" enums:"agent.session.turn" binding:"required"`
	Status      string      `json:"status" enums:"queued,in_progress,waiting,completed,failed,cancelled" binding:"required"`
	CreatedAt   int64       `json:"created_at" binding:"required"`
	StartedAt   *int64      `json:"started_at" extensions:"x-nullable"`
	CompletedAt *int64      `json:"completed_at" extensions:"x-nullable"`
	Error       *TurnError  `json:"error" extensions:"x-nullable"`
	Usage       *TokenUsage `json:"usage" extensions:"x-nullable"`
}

type TurnError struct {
	Code    string `json:"code" enums:"internal_error" binding:"required"`
	Message string `json:"message" binding:"required"`
}

type TurnList struct {
	Data    []Turn `json:"data" binding:"required"`
	HasMore bool   `json:"has_more" binding:"required"`
}
