package proto

const (
	TypeWorkspaceRead       = "workspace_read"
	TypeWorkspaceReadResult = "workspace_read_result"
	WorkspaceReadMaxBytes   = 1 << 20
)

// WorkspaceReadPayload targets one existing resource on the current daemon connection.
type WorkspaceReadPayload struct {
	Handle        string `json:"handle,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	EnvironmentID string `json:"environment_id"`
	Path          string `json:"path"`
	MaxBytes      int    `json:"max_bytes"`
}

// WorkspaceReadResultPayload never infers file settlement from local process exit.
type WorkspaceReadResultPayload struct {
	Outcome           string `json:"outcome"`
	Data              []byte `json:"data,omitempty"`
	Truncated         bool   `json:"truncated,omitempty"`
	CloseAcknowledged bool   `json:"close_acknowledged,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
}
