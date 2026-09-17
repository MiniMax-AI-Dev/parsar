package codex

import (
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// The deployment selects a native named profile; request options cannot select it.
// Native managed requirements must constrain its definition and allowed profiles.
// This selection does not establish workspace authority or public admission.
func validatePermissionProfile(req proto.PromptRequestPayload, profile string) error {
	if profile == "" {
		return nil
	}
	if strings.TrimSpace(profile) != profile || strings.HasPrefix(profile, ":") {
		return errors.New("codex: deployment permissions require a named native profile")
	}
	if req.RemoteEnvironment != nil || req.DisableExecutionEnvironment || req.WorkspaceReadOnly {
		return errors.New("codex: deployment permission profile requires local execution")
	}
	return nil
}
