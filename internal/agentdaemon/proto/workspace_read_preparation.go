package proto

// ValidWorkspaceReadPreparation excludes execution configuration and local paths.
// The native adapter supplies temporary state; this request cannot resume or start.
func ValidWorkspaceReadPreparation(r PromptRequestPayload) bool {
	return r.WorkspaceReadOnly && ((r.RemoteEnvironment != nil) != (r.LocalEnvironment != nil)) && r.AgentStateKey != "" &&
		r.StrictResume && r.ReleaseOnCompletion && r.RunID == "" && r.Prompt == "" &&
		r.ConversationID == "" && r.AgentSessionID == "" && r.WorkDir == "" &&
		!r.WorkspaceAuthoring && !r.DisableExecutionEnvironment && len(r.Attachments) == 0 &&
		len(r.AgentOptions) == 0 && r.ExecutionControls == nil && r.MCPHTTPServers == nil &&
		len(r.FunctionTools) == 0 && !r.ObserveMessages && !r.ObserveTools &&
		!r.ObserveToolObservations && !r.ObserveSubagentIdentities
}
