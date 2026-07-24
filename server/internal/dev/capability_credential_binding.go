package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type credentialBindingSecretStore interface {
	GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error)
}

type capabilityCredentialBindingValidationInput struct {
	WorkspaceID     string
	AgentVisibility string
	AgentConfig     map[string]any
	Version         store.CapabilityVersionRead
	Configuration   map[string]any
}

func validateCapabilityCredentialBindings(
	ctx context.Context,
	secretStore credentialBindingSecretStore,
	input capabilityCredentialBindingValidationInput,
) error {
	bindings, err := credentialbinding.ParseStrict(input.Configuration)
	if err != nil {
		return fmt.Errorf("configuration.%w", err)
	}
	requiredKinds := make(map[string]bool, len(input.Version.RequiredCredentials))
	for _, required := range input.Version.RequiredCredentials {
		kind := strings.TrimSpace(required.Kind)
		if required.Required && kind != "" {
			requiredKinds[kind] = true
		}
	}
	for kind := range bindings {
		if !requiredKinds[kind] {
			return errors.New("credential binding kind is not required by this capability")
		}
	}

	agentBindings := credentialbinding.ParseLenient(input.AgentConfig)
	for kind := range requiredKinds {
		binding, configured := bindings[kind]
		if !configured {
			binding, configured = agentBindings[kind]
		}
		if !configured || binding.Source == credentialbinding.SourcePersonal {
			if strings.TrimSpace(input.AgentVisibility) == agentVisibilityPublic {
				return errors.New("public agents require a shared secret for every capability credential")
			}
			continue
		}
		if err := validateSharedCapabilityCredential(ctx, secretStore, input.WorkspaceID, kind, binding.SecretID, input.Version.SourcePayload); err != nil {
			return err
		}
	}
	return nil
}

func validateSharedCapabilityCredential(
	ctx context.Context,
	secretStore credentialBindingSecretStore,
	workspaceID string,
	kind string,
	secretID string,
	sourcePayload json.RawMessage,
) error {
	secretID = strings.TrimSpace(secretID)
	if !isUUID(secretID) {
		return errors.New("credential binding secret_id must be a valid uuid")
	}
	secret, err := secretStore.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil || secret.Status != "active" || secret.Kind != "capability_inline" {
		return errors.New("credential binding secret is unavailable")
	}
	secretKind := strings.TrimSpace(metadataStringValue(secret.Metadata, "credential_kind_code"))
	if secretKind != "" && secretKind != kind {
		return errors.New("credential binding secret has the wrong credential kind")
	}
	catalogID := catalogIDFromSourcePayload(sourcePayload)
	if kind == capability.CredentialKindMCPOAuth && catalogID != "" &&
		(secretKind != kind || secret.AuthType != "oauth2" || strings.TrimSpace(secret.Provider) != catalogID) {
		return errors.New("credential binding secret belongs to a different MCP connector")
	}
	return nil
}

func metadataStringValue(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func catalogIDFromSourcePayload(sourcePayload json.RawMessage) string {
	if len(sourcePayload) == 0 {
		return ""
	}
	var source struct {
		SourceFormat string `json:"source_format"`
		CatalogID    string `json:"catalog_id"`
	}
	if err := json.Unmarshal(sourcePayload, &source); err != nil || source.SourceFormat != "mcp_catalog" {
		return ""
	}
	return strings.TrimSpace(source.CatalogID)
}
