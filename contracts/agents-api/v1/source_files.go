package v1

type SourceFile struct {
	ID            string  `json:"id" binding:"required"`
	Object        string  `json:"object" binding:"required" enums:"file"`
	Bytes         int64   `json:"bytes" binding:"required" minimum:"0"`
	CreatedAt     int64   `json:"created_at" binding:"required"`
	Filename      string  `json:"filename" binding:"required"`
	Purpose       string  `json:"purpose" binding:"required" enums:"user_data"`
	Status        string  `json:"status" binding:"required" enums:"processed"`
	ExpiresAt     *int64  `json:"expires_at" extensions:"x-nullable"`
	StatusDetails *string `json:"status_details" extensions:"x-nullable"`
}

type SourceFileList struct {
	Object  string       `json:"object" binding:"required" enums:"list"`
	Data    []SourceFile `json:"data" binding:"required"`
	HasMore bool         `json:"has_more" binding:"required"`
	FirstID *string      `json:"first_id" extensions:"x-nullable"`
	LastID  *string      `json:"last_id" extensions:"x-nullable"`
}

type SourceFileDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"file"`
	Deleted bool   `json:"deleted" binding:"required"`
}
