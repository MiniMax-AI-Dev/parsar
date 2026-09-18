package v1

import "encoding/json"

// SessionEnvironment contains supported output variants, independently of request admission.
type SessionEnvironment struct {
	Type                  string               `json:"type" enums:"none,self_hosted,openai_hosted" binding:"required"`
	ID                    string               `json:"id,omitempty"`
	CapabilityDirectories *[]string            `json:"capability_directories,omitempty"`
	RemoteURL             string               `json:"remote_url,omitempty"`
	WorkspaceDirectory    string               `json:"workspace_directory,omitempty"`
	Network               *EnvironmentNetwork  `json:"network,omitempty"`
	Packages              *EnvironmentPackages `json:"packages,omitempty"`
	Files                 *[]json.RawMessage   `json:"files,omitempty" swaggertype:"array,object"`
	Plugins               *[]json.RawMessage   `json:"plugins,omitempty" swaggertype:"array,object"`
	Skills                *[]json.RawMessage   `json:"skills,omitempty" swaggertype:"array,object"`
}
