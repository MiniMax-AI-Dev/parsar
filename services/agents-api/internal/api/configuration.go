package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type configuration struct {
	Agent          v1.Agent                     `json:"agent"`
	Environment    v1.Environment               `json:"environment"`
	VaultIDs       []string                     `json:"vault_ids,omitempty"`
	MCPCredentials []store.MCPCredentialBinding `json:"mcp_credentials,omitempty"`
}

func resolve(input sessionRequest, tenant, key string, saved *v1.SavedAgent) (json.RawMessage, error) {
	if input.Environment == nil || (input.Environment.Type != "none" && input.Environment.Type != "self_hosted" && input.Environment.Type != "openai_hosted") {
		return nil, errors.New("Unsupported environment type.")
	}
	if err := validateMetadata(input.Metadata); err != nil {
		return nil, err
	}
	agent, err := resolveSessionAgent(input, saved)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		// Inline execution configuration has its own stable identity for creation retries.
		agent.ID = "agent_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant+"\x00"+key)).String()
	} else {
		agent.ID = saved.ID
	}
	return json.Marshal(configuration{Agent: agent, Environment: *input.Environment, VaultIDs: input.VaultIDs})
}
