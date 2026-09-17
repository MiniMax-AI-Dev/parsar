package v1

// EnvironmentFileCreateRequest represents the pinned inline/file_id union.
type EnvironmentFileCreateRequest struct {
	Type   string  `json:"type" binding:"required" enums:"inline,file_id"`
	Data   *string `json:"data,omitempty"`
	FileID *string `json:"file_id,omitempty"`
	Path   *string `json:"path" binding:"required"`
}

type EnvironmentFile struct {
	EnvironmentID string `json:"environment_id" binding:"required"`
	Object        string `json:"object" binding:"required" enums:"agent.environment.file"`
	Path          string `json:"path" binding:"required"`
	SizeBytes     int64  `json:"size_bytes" binding:"required" minimum:"0"`
}

type EnvironmentFileList struct {
	Data []EnvironmentFile `json:"data" binding:"required"`
	Next *string           `json:"next" extensions:"x-nullable"`
}
