package v1

// CreateVaultRequest creates a project-owned credential container. An omitted
// name stays null; a supplied name must be a string with 1–256 UTF-8 bytes after trimming.
type CreateVaultRequest struct {
	Name     *string            `json:"name,omitempty"`
	Metadata map[string]*string `json:"metadata,omitempty" swaggertype:"object,string" extensions:"x-nullable"`
}

type Vault struct {
	ID        string            `json:"id" binding:"required"`
	Object    string            `json:"object" binding:"required" enums:"vault"`
	CreatedAt int64             `json:"created_at" binding:"required"`
	Name      *string           `json:"name" extensions:"x-nullable"`
	Metadata  map[string]string `json:"metadata" binding:"required"`
}

type VaultList struct {
	Object  string  `json:"object" binding:"required" enums:"list"`
	Data    []Vault `json:"data" binding:"required"`
	HasMore bool    `json:"has_more" binding:"required"`
	FirstID *string `json:"first_id" extensions:"x-nullable"`
	LastID  *string `json:"last_id" extensions:"x-nullable"`
}

type VaultDeleted struct {
	ID      string `json:"id" binding:"required"`
	Deleted bool   `json:"deleted" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"vault.deleted"`
}
