package v1

import "encoding/json"

// EnvironmentTemplateRequest exposes the pinned input fields. Populated installations
// and restricted networking are rejected until their initialization is qualified.
type EnvironmentTemplateRequest struct {
	Name                  *string                  `json:"name,omitempty" extensions:"x-nullable"`
	Network               *EnvironmentNetworkInput `json:"network,omitempty" extensions:"x-nullable"`
	CapabilityDirectories []string                 `json:"capability_directories,omitempty"`
	Env                   map[string]string        `json:"env,omitempty"`
	Files                 []json.RawMessage        `json:"files,omitempty" swaggertype:"array,object"`
	Packages              *EnvironmentPackages     `json:"packages,omitempty"`
	Plugins               []json.RawMessage        `json:"plugins,omitempty" swaggertype:"array,object"`
	Skills                []json.RawMessage        `json:"skills,omitempty" swaggertype:"array,object"`
	SetupCommands         []json.RawMessage        `json:"setup_commands,omitempty" swaggertype:"array,object"`
}

// EnvironmentTemplate returns safe configuration metadata only.
type EnvironmentTemplate struct {
	ID                    string              `json:"id" binding:"required"`
	Object                string              `json:"object" binding:"required" enums:"agent.environment.template"`
	Name                  *string             `json:"name" extensions:"x-nullable"`
	CreatedAt             int64               `json:"created_at" binding:"required"`
	UpdatedAt             int64               `json:"updated_at" binding:"required"`
	CapabilityDirectories []string            `json:"capability_directories" binding:"required"`
	Network               EnvironmentNetwork  `json:"network" binding:"required"`
	Packages              EnvironmentPackages `json:"packages" binding:"required"`
	Files                 []json.RawMessage   `json:"files" binding:"required" swaggertype:"array,object"`
	Plugins               []json.RawMessage   `json:"plugins" binding:"required" swaggertype:"array,object"`
	Skills                []json.RawMessage   `json:"skills" binding:"required" swaggertype:"array,object"`
}

type EnvironmentTemplateList struct {
	Object  string                `json:"object" binding:"required" enums:"list"`
	Data    []EnvironmentTemplate `json:"data" binding:"required"`
	HasMore bool                  `json:"has_more" binding:"required"`
	FirstID *string               `json:"first_id" extensions:"x-nullable"`
	LastID  *string               `json:"last_id" extensions:"x-nullable"`
}

type EnvironmentTemplateDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"agent.environment.template.deleted"`
	Deleted bool   `json:"deleted" binding:"required"`
}
