package proto

// Preparation controls use a caller request ID on Envelope.ID, never a RunID.
// Handles are daemon-generated and valid only on the accepting connection.
const (
	TypeExecutionPrepare  = "execution_prepare"
	TypeExecutionStart    = "execution_start"
	TypeExecutionRelease  = "execution_release"
	TypePreparationStatus = "preparation_status"
)

// ExecutionPreparePayload reuses execution configuration without accepting input
// or product authoring. The initial private profile requires a remote environment,
// stable state key, strict resume and completion release.
type ExecutionPreparePayload struct {
	Configuration PromptRequestPayload `json:"configuration"`
}

type ExecutionStartPayload struct {
	Handle string `json:"handle"`
	RunID  string `json:"run_id"`
	Prompt string `json:"prompt"`
}

type ExecutionReleasePayload struct {
	Handle string `json:"handle"`
}

// PreparationStatusPayload is a connection-local observation, not a public event.
// Keep the highest Revision for each Handle; concurrent sends may arrive out of
// order. ExpiresAt is Unix milliseconds. Repeated requests do not extend it. Retired request IDs
// may allocate a fresh handle, but an old handle can never start its replacement.
type PreparationStatusPayload struct {
	Handle    string `json:"handle,omitempty"`
	Revision  uint64 `json:"revision"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	// Operation is supplied for a rejected control operation (no resource revision).
	Operation string `json:"operation,omitempty"`
}
