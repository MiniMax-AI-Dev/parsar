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
	if req.LocalEnvironment != nil && profile == "" {
		return errors.New("codex: local Environment requires deployment-managed permissions")
	}
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

// managedPermissionProfile maps a validated deployment binding to native policy.
// Public or prompt options cannot choose a native profile or widen that binding.
func managedPermissionProfile(req proto.PromptRequestPayload, cfg sessionConfig) (string, error) {
	profile := cfg.permissionProfile
	if cfg.runtimeNetworkAccess != "" {
		if req.LocalEnvironment == nil || req.LocalEnvironment.NetworkAccess != cfg.runtimeNetworkAccess || profile != "managed-workspace" {
			return "", errors.New("codex: Runtime network policy mismatch")
		}
		switch cfg.runtimeNetworkAccess {
		case "disabled":
		case "enabled":
			profile = "managed-workspace-enabled"
		default:
			return "", errors.New("codex: unsupported Runtime network policy")
		}
	} else if req.LocalEnvironment != nil && req.LocalEnvironment.NetworkAccess != "" {
		return "", errors.New("codex: Runtime has no bound network policy")
	}
	return profile, validatePermissionProfile(req, profile)
}
