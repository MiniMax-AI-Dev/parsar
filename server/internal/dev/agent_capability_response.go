package dev

import (
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func installedAgentCapabilityResponse(binding store.AgentCapabilityRead, capability store.EnabledCapabilityRead, workspaceID string) map[string]any {
	requiredCredentials := capability.RequiredCredentials
	if capability.Type == "mcp" && strings.TrimSpace(binding.PinningMode) == store.PinningModeLatest {
		requiredCredentials = capability.LatestRequiredCredentials
	}
	return map[string]any{
		"id":                    binding.ID,
		"agent_id":              binding.AgentID,
		"capability_id":         binding.CapabilityID,
		"capability_version_id": binding.CapabilityVersionID,
		"pinning_mode":          binding.PinningMode,
		"enabled":               binding.Enabled,
		"configuration":         binding.Configuration,
		"created_at":            binding.CreatedAt,
		"updated_at":            binding.UpdatedAt,
		"capability": map[string]any{
			"id":                        capability.CapabilityID,
			"workspace_id":              capability.WorkspaceID,
			"type":                      capability.Type,
			"name":                      capability.Name,
			"description":               capability.Description,
			"visibility":                capability.Visibility,
			"status":                    capability.Status,
			"required_credentials":      requiredCredentials,
			"deprecated_at":             capability.DeprecatedAt,
			"from_marketplace":          capability.WorkspaceID != workspaceID,
			"source_workspace_id":       capability.WorkspaceID,
			"source_workspace_name":     capability.SourceWorkspaceName,
			"latest_version_id":         capability.LatestVersionID,
			"latest_version":            capability.LatestVersion,
			"latest_version_created_at": capability.LatestVersionCreatedAt,
			"pinned_version_id":         binding.CapabilityVersionID,
			"pinned_version":            capability.Version,
			"created_at":                capability.LatestVersionCreatedAt,
			"updated_at":                capability.LatestVersionCreatedAt,
		},
	}
}
