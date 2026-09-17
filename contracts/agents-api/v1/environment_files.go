package v1

// InlineEnvironmentFileCreateRequest is the implemented member of the pinned
// inline/file_id union. Source-file resolution remains a separate implementation gap.
type InlineEnvironmentFileCreateRequest struct {
	Type string  `json:"type" binding:"required" enums:"inline"`
	Data *string `json:"data" binding:"required"`
	Path *string `json:"path" binding:"required"`
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
