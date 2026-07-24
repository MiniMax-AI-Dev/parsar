package dev

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type credentialBindingSecretStub struct {
	provider string
}

func (s credentialBindingSecretStub) GetSecretPayload(context.Context, string, string) (store.SecretPayload, error) {
	return store.SecretPayload{SecretRead: store.SecretRead{
		Kind:     "capability_inline",
		Provider: s.provider,
		AuthType: "oauth2",
		Status:   "active",
		Metadata: map[string]any{"credential_kind_code": capability.CredentialKindMCPOAuth},
	}}, nil
}

func TestValidateCapabilityCredentialBindingsChecksAgentFallbackProvider(t *testing.T) {
	input := capabilityCredentialBindingValidationInput{
		WorkspaceID: "00000000-0000-0000-0000-000000000002",
		AgentConfig: map[string]any{
			"credential_bindings": map[string]any{
				capability.CredentialKindMCPOAuth: map[string]any{
					"source":    "shared",
					"secret_id": "00000000-0000-0000-0000-000000000099",
				},
			},
		},
		Version: store.CapabilityVersionRead{
			SourcePayload:       json.RawMessage(`{"source_format":"mcp_catalog","catalog_id":"notion"}`),
			RequiredCredentials: []store.RequiredCredential{{Kind: capability.CredentialKindMCPOAuth, Required: true}},
		},
	}

	err := validateCapabilityCredentialBindings(context.Background(), credentialBindingSecretStub{provider: "github"}, input)
	if err == nil || !strings.Contains(err.Error(), "different MCP connector") {
		t.Fatalf("provider mismatch error = %v", err)
	}
	if err := validateCapabilityCredentialBindings(context.Background(), credentialBindingSecretStub{provider: "notion"}, input); err != nil {
		t.Fatalf("matching provider returned error: %v", err)
	}
}
