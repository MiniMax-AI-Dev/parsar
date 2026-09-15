package execution

import (
	"encoding/json"
	"errors"
	"net/url"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Resolve only the frozen decision. Never search current Vault contents during
// dispatch: a later credential must not change an anonymous or selected server.
func selectedMCPCredentials(snapshot Snapshot) (map[string]store.MCPCredentialBinding, error) {
	invalid := errors.New("invalid frozen MCP credential binding")
	tools := map[string]v1.MCPTool{}
	for _, raw := range snapshot.Agent.Tools {
		var tool v1.MCPTool
		if json.Unmarshal(raw, &tool) != nil {
			return nil, invalid
		}
		if tool.Type == "mcp" {
			tools[tool.ServerLabel] = tool
		}
	}
	attached := map[uuid.UUID]bool{}
	for _, raw := range snapshot.VaultIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, invalid
		}
		attached[id] = true
	}
	seen := map[string]bool{}
	selected := map[string]store.MCPCredentialBinding{}
	for _, binding := range snapshot.MCPCredentials {
		tool, exists := tools[binding.ServerLabel]
		if !exists || seen[binding.ServerLabel] || tool.Transport.ServerURL != binding.ServerURL {
			return nil, invalid
		}
		seen[binding.ServerLabel] = true
		if binding.CredentialID == "" {
			if binding.VaultID != "" || binding.AuthType != "" || tool.CredentialID != nil {
				return nil, invalid
			}
			continue
		}
		vaultID, err := uuid.Parse(binding.VaultID)
		if err != nil || !attached[vaultID] || binding.AuthType != "static_bearer" {
			return nil, invalid
		}
		id, err := uuid.Parse(binding.CredentialID)
		if err != nil {
			return nil, invalid
		}
		if tool.CredentialID != nil {
			declared, err := uuid.Parse(*tool.CredentialID)
			if err != nil || declared != id {
				return nil, invalid
			}
		}
		endpoint, err := url.Parse(binding.ServerURL)
		if err != nil || endpoint.Scheme != "https" {
			return nil, invalid
		}
		selected[binding.ServerLabel] = binding
	}
	for label, tool := range tools {
		if !seen[label] && (tool.CredentialID != nil || len(snapshot.VaultIDs) > 0 || len(snapshot.MCPCredentials) > 0) {
			return nil, invalid
		}
	}
	return selected, nil
}
