package v1

// CreateCredentialRequest is the initial static-bearer resource profile. OAuth
// remains a separate missing union member, not a change to the upstream target.
type CreateCredentialRequest struct {
	Name *string                      `json:"name" binding:"required"`
	Auth *StaticBearerCredentialInput `json:"auth" binding:"required"`
}

type StaticBearerCredentialInput struct {
	Type         string  `json:"type" binding:"required" enums:"static_bearer"`
	MCPServerURL *string `json:"mcp_server_url" binding:"required"`
	Token        *string `json:"token" binding:"required"`
}

// UpdateCredentialRequest implements static token replacement. The pinned OAuth
// replacement variant remains a separate missing union member.
type UpdateCredentialRequest struct {
	Auth *StaticBearerCredentialReplacement `json:"auth" binding:"required"`
}

type StaticBearerCredentialReplacement struct {
	Type  string  `json:"type" binding:"required" enums:"static_bearer"`
	Token *string `json:"token" binding:"required"`
}

type StaticBearerCredentialAuth struct {
	Type         string `json:"type" binding:"required" enums:"static_bearer"`
	MCPServerURL string `json:"mcp_server_url" binding:"required"`
}

type Credential struct {
	ID        string                     `json:"id" binding:"required"`
	VaultID   string                     `json:"vault_id" binding:"required"`
	Name      string                     `json:"name" binding:"required"`
	Object    string                     `json:"object" binding:"required" enums:"vault.credential"`
	Auth      StaticBearerCredentialAuth `json:"auth" binding:"required"`
	CreatedAt int64                      `json:"created_at" binding:"required"`
	UpdatedAt int64                      `json:"updated_at" binding:"required"`
}

type CredentialList struct {
	Object  string       `json:"object" binding:"required" enums:"list"`
	Data    []Credential `json:"data" binding:"required"`
	HasMore bool         `json:"has_more" binding:"required"`
	FirstID *string      `json:"first_id" extensions:"x-nullable"`
	LastID  *string      `json:"last_id" extensions:"x-nullable"`
}

type CredentialDeleted struct {
	ID      string `json:"id" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"vault.credential.deleted"`
}
