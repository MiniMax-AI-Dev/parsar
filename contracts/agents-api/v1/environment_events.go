package v1

// SessionEnvironmentState is the pinned event snapshot, not EnvironmentInfo.
// The event vocabulary includes ready; the resource vocabulary instead includes expired.
type SessionEnvironmentState struct {
	ID     string       `json:"id" binding:"required"`
	Type   string       `json:"type" binding:"required"`
	Status string       `json:"status" enums:"pending,ready,connected,disconnected,failed" binding:"required"`
	Error  *StreamError `json:"error" extensions:"x-nullable"`
}
