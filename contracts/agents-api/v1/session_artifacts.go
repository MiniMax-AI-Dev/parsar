package v1

type SessionArtifact struct {
	ID            string `json:"id" binding:"required"`
	CreatedAt     int64  `json:"created_at" binding:"required"`
	EnvironmentID string `json:"environment_id" binding:"required"`
	Object        string `json:"object" enums:"agent.session.artifact" binding:"required"`
	Path          string `json:"path" binding:"required"`
	SessionID     string `json:"session_id" binding:"required"`
	SizeBytes     int64  `json:"size_bytes" binding:"required"`
	TurnID        string `json:"turn_id" binding:"required"`
}

type SessionArtifactList struct {
	Data    []SessionArtifact `json:"data" binding:"required"`
	HasMore bool              `json:"has_more" binding:"required"`
}

type SessionArtifactDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" enums:"agent.session.artifact.deleted" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
}
