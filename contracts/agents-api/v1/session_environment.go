package v1

// SessionEnvironment contains supported output variants, independently of request admission.
type SessionEnvironment struct {
	Type                  string    `json:"type" enums:"none,self_hosted" binding:"required"`
	ID                    string    `json:"id,omitempty"`
	CapabilityDirectories *[]string `json:"capability_directories,omitempty"`
	RemoteURL             string    `json:"remote_url,omitempty"`
	WorkspaceDirectory    string    `json:"workspace_directory,omitempty"`
}
