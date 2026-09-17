package proto

// LocalEnvironment references a deployment-bound workspace; it never supplies a path.
type LocalEnvironment struct {
	ID string `json:"id"`
}

func (r PromptRequestPayload) EnvironmentID() string {
	if r.LocalEnvironment != nil {
		return r.LocalEnvironment.ID
	}
	if r.RemoteEnvironment != nil {
		return r.RemoteEnvironment.ID
	}
	return ""
}

// RemoteEnvironment is a transient execution binding, not a public Environment
// resource. WorkDir remains the harness-local cwd. The selected AgentKind owns
// the native connection protocol; no native selector or configuration-variable name is shared.
// Send only to a peer advertising remote_environment. Never persist or log the
// connection token in Session configuration, events or completion metadata.
type RemoteEnvironment struct {
	ID                 string `json:"id"`
	WorkspaceDirectory string `json:"workspace_directory"`
	ConnectionURL      string `json:"connection_url"`
	ConnectionToken    string `json:"connection_token"`
}
