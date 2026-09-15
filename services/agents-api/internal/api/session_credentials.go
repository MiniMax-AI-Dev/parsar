package api

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type mcpCredentialResolver interface {
	ResolveMCPCredentials(context.Context, string, []string, []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error)
}

func (h *Handler) bindSessionCredentials(ctx context.Context, tenant string, raw json.RawMessage) (json.RawMessage, error) {
	var cfg configuration
	if json.Unmarshal(raw, &cfg) != nil {
		return nil, store.ErrInvalidInput
	}
	var requests []store.MCPCredentialRequest
	required := len(cfg.VaultIDs) > 0
	for _, rawTool := range cfg.Agent.Tools {
		var tool v1.MCPTool
		if json.Unmarshal(rawTool, &tool) != nil {
			return nil, store.ErrInvalidInput
		}
		if tool.Type == "mcp" {
			requests = append(requests, store.MCPCredentialRequest{ServerLabel: tool.ServerLabel, ServerURL: tool.Transport.ServerURL, CredentialID: tool.CredentialID})
			required = required || tool.CredentialID != nil
		}
	}
	if !required {
		return raw, nil
	}
	resolver, ok := h.store.(mcpCredentialResolver)
	if !ok {
		return nil, store.ErrCredentialStorageUnavailable
	}
	bindings, err := resolver.ResolveMCPCredentials(ctx, tenant, cfg.VaultIDs, requests)
	if err != nil {
		return nil, err
	}
	cfg.MCPCredentials = bindings
	return json.Marshal(cfg)
}

// Attached inline requests need recorded caller intent before reading mutable
// Vault contents. Other inline requests retain their resolved/default identity.
func inlineCredentialIntent(input sessionRequest) bool {
	if len(input.VaultIDs) > 0 {
		return true
	}
	var tools []struct {
		Type         string  `json:"type"`
		CredentialID *string `json:"credential_id"`
	}
	if json.Unmarshal(input.agentFields["tools"], &tools) == nil {
		for _, tool := range tools {
			if tool.Type == "mcp" && tool.CredentialID != nil {
				return true
			}
		}
	}
	return false
}
