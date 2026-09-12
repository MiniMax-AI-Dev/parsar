package v1

// FunctionCallAction is the supported function-call variant of required_actions.
type FunctionCallAction struct {
	Arguments any    `json:"arguments" binding:"required"`
	CallID    string `json:"call_id" binding:"required"`
	Name      string `json:"name" binding:"required"`
	TurnID    string `json:"turn_id" binding:"required"`
	Type      string `json:"type" enums:"function_call" binding:"required"`
}
