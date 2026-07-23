package mcpdirectory

import (
	"context"

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
) (store.SecretPayload, error) {
	payload := credential.Payload()
	payload["catalog_id"] = item.ID
	encrypted, err := h.deps.Secrets.Encrypt(payload)
	if err != nil {
		return store.SecretPayload{}, err
	}
	if existing, found, err := h.workspaceOAuthCredentialRead(ctx, workspaceID, item, false); err != nil {
		return store.SecretPayload{}, err
	} else if found {
		return h.deps.WorkspaceCredentials.UpdateSecretPayload(ctx, workspaceID, existing.ID, encrypted)
	}
	created, err := h.deps.WorkspaceCredentials.CreateSecret(ctx, store.CreateSecretInput{
		WorkspaceID:        workspaceID,
		Name:               item.Name + " OAuth",
		Kind:               "capability_inline",
		Provider:           item.ID,
		AuthType:           "oauth2",
		Masked:             secrets.MaskPayload(payload),
		CreatedBy:          createdBy,
		CredentialKindCode: item.Authentication.CredentialKind,
		Metadata: map[string]any{
			"catalog_id": item.ID,
		},
	}, encrypted)
	if err != nil {
		return store.SecretPayload{}, err
	}
	return store.SecretPayload{SecretRead: created, EncryptedPayload: encrypted}, nil
}

func (h *handler) workspaceOAuthCredential(
	ctx context.Context,
	workspaceID string,
	item mcpcatalog.Item,
) (store.SecretPayload, bool, error) {
	read, found, err := h.workspaceOAuthCredentialRead(ctx, workspaceID, item, true)
	if err != nil || !found {
		return store.SecretPayload{}, found, err
	}
	payload, err := h.deps.WorkspaceCredentials.GetSecretPayload(ctx, workspaceID, read.ID)
	return payload, err == nil, err
}

func (h *handler) workspaceOAuthCredentialRead(
	ctx context.Context,
	workspaceID string,
	item mcpcatalog.Item,
	activeOnly bool,
) (store.SecretRead, bool, error) {
	workspaceSecrets, err := h.deps.WorkspaceCredentials.ListSecrets(ctx, workspaceID, 1000)
	if err != nil {
		return store.SecretRead{}, false, err
	}
	for _, candidate := range workspaceSecrets {
		if activeOnly && candidate.Status != "active" {
			continue
		}
		if candidate.Kind != "capability_inline" ||
			candidate.AuthType != "oauth2" ||
			metadataString(candidate.Metadata, "workspace_id") != workspaceID ||
			metadataString(candidate.Metadata, "credential_kind_code") != item.Authentication.CredentialKind ||
			metadataString(candidate.Metadata, "catalog_id") != item.ID {
			continue
		}
		return candidate, true, nil
	}
	return store.SecretRead{}, false, nil
}

func (h *handler) verifyStoredWorkspaceOAuthCredential(
	ctx context.Context,
	workspaceID string,
	item mcpcatalog.Item,
	stored store.SecretPayload,
) (oauthConnectionResponse, error) {
	payload, err := h.deps.Secrets.Decrypt(stored.EncryptedPayload)
	if err != nil {
		return oauthConnectionResponse{}, err
	}
	payload, verification, err := h.verifyOAuthPayload(ctx, item, payload)
	if err != nil {
		return oauthConnectionResponse{}, err
	}
	mcpoauth.ApplyVerification(payload, verification)
	encrypted, err := h.deps.Secrets.Encrypt(payload)
	if err != nil {
		return oauthConnectionResponse{}, err
	}
	if _, err := h.deps.WorkspaceCredentials.UpdateSecretPayload(ctx, workspaceID, stored.ID, encrypted); err != nil {
		return oauthConnectionResponse{}, err
	}
	return oauthConnectionResult(verification), nil
}
