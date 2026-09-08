package dev

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type agentCapabilityStore interface {
	ListAgentCapabilities(ctx context.Context, agentID string) ([]store.AgentCapabilityRead, error)
	GetEnabledMarketplaceCapabilitiesForAgent(ctx context.Context, agentID string) ([]store.EnabledCapabilityRead, error)
	EnableAgentCapability(ctx context.Context, agentID string, versionID string, configuration map[string]any, pinningMode string) (store.AgentCapabilityRead, error)
	UpgradeAgentCapability(ctx context.Context, agentID string, capabilityID string, newVersionID string, pinningMode string) (store.AgentCapabilityRead, error)
	UninstallWorkspaceMarketplaceCapability(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) (int64, error)
	DeleteAgentCapability(ctx context.Context, agentID string, capabilityVersionID string) error
	RecordAgentCapabilityAudit(input store.AgentCapabilityAuditInput)
}
