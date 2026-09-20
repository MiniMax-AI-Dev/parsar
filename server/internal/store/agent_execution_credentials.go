package store

import (
	"context"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
)

func validModelCredentialKind(kind string) bool {
	return kind == "openai_api_key" || kind == "anthropic_api_key"
}

// ResolveAgentCredential uses the conversation caller, never the Agent creator.
func (s *Store) ResolveAgentCredential(ctx context.Context, workspaceID, userID, kind string, binding credentialbinding.Binding) (string, error) {
	unavailable := errors.New("selected credential is missing or unavailable")
	if !validModelCredentialKind(kind) {
		return "", unavailable
	}
	var encrypted []byte
	if binding.Source == credentialbinding.SourceShared {
		if _, err := uuid(binding.SecretID); err != nil {
			return "", unavailable
		}
		secret, err := s.GetSecretPayload(ctx, workspaceID, binding.SecretID)
		if err != nil || secret.Status != "active" || secret.Kind != "capability_inline" || secret.AuthType == "oauth2" {
			return "", unavailable
		}
		secretKind, _ := secret.Metadata["credential_kind_code"].(string)
		if secretKind != "" && secretKind != kind {
			return "", unavailable
		}
		encrypted = secret.EncryptedPayload
	} else {
		if _, err := uuid(userID); err != nil {
			return "", unavailable
		}
		credential, found, err := s.GetUserCredentialByUserKind(ctx, userID, kind)
		if err != nil || !found {
			return "", unavailable
		}
		encrypted = credential.Ciphertext
	}
	cipher, err := catalogCipher()
	if err != nil {
		return "", unavailable
	}
	payload, err := cipher.Decrypt(encrypted)
	if err != nil {
		return "", unavailable
	}
	value, _ := payload["value"].(string)
	if strings.TrimSpace(value) == "" {
		return "", unavailable
	}
	return value, nil
}
