package mcpdirectory

import (
	"context"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/mcpoauth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/mcpcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func (h *handler) saveWorkspaceOAuthCredential(
	ctx context.Context,
	workspaceID string,
	item mcpcatalog.Item,
	credential mcpoauth.Credential,
	createdBy string,
) error {
	payload := credential.Payload()
	encrypted, err := h.deps.Secrets.Encrypt(payload)
	if err != nil {
		return err
	}
	existing, found, err := h.workspaceOAuthCredentialRead(ctx, workspaceID, item)
	if err != nil {
		return err
	}
	if found {
		_, err = h.deps.WorkspaceCredentials.UpdateSecretPayload(ctx, workspaceID, existing.ID, encrypted)
		return err
	}
	_, err = h.deps.WorkspaceCredentials.CreateSecret(ctx, store.CreateSecretInput{
		WorkspaceID:        workspaceID,
		Name:               item.Name + " OAuth",
		Kind:               "capability_inline",
		Provider:           item.ID,
		AuthType:           "oauth2",
		Masked:             secrets.MaskPayload(payload),
		CreatedBy:          createdBy,
		CredentialKindCode: item.Authentication.CredentialKind,
	}, encrypted)
	return err
}

func (h *handler) workspaceOAuthCredentialRead(
	ctx context.Context,
	workspaceID string,
	item mcpcatalog.Item,
) (store.SecretRead, bool, error) {
	workspaceSecrets, err := h.deps.WorkspaceCredentials.ListSecrets(ctx, workspaceID, 1000)
	if err != nil {
		return store.SecretRead{}, false, err
	}
	for _, candidate := range workspaceSecrets {
		if candidate.Kind != "capability_inline" ||
			candidate.AuthType != "oauth2" ||
			metadataString(candidate.Metadata, "workspace_id") != strings.TrimSpace(workspaceID) ||
			strings.TrimSpace(candidate.Provider) != item.ID ||
			metadataString(candidate.Metadata, "credential_kind_code") != item.Authentication.CredentialKind {
			continue
		}
		return candidate, true, nil
	}
	return store.SecretRead{}, false, nil
}
