package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UpdateStaticCredentialInput struct {
	Token string
}

// UpdateStaticCredential replaces only the secret and update time. Safe metadata
// supplies immutable AAD; the mutation independently checks that same scope.
// A subsequent dispatch reads the replacement through the existing frozen binding.
func (s *Store) UpdateStaticCredential(ctx context.Context, tenantID, vaultID, credentialID string, input UpdateStaticCredentialInput) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, ErrNotFound
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return Credential{}, ErrNotFound
	}
	id, err := parseID(credentialID)
	if err != nil {
		return Credential{}, ErrNotFound
	}
	current, err := s.GetCredential(ctx, tenantID, vaultID, credentialID)
	if err != nil {
		return Credential{}, err
	}
	if current.AuthType != "static_bearer" {
		return Credential{}, ErrInvalidInput
	}
	if s.credentialCipher == nil {
		return Credential{}, ErrCredentialStorageUnavailable
	}
	ciphertext, err := s.credentialCipher.Seal([]byte(input.Token), credentialcrypto.Binding{
		TenantID: uuid.UUID(tenant.Bytes).String(), VaultID: current.VaultID,
		CredentialID: current.ID, AuthType: current.AuthType, Destination: current.MCPServerURL,
	})
	if err != nil {
		return Credential{}, errors.New("credential encryption failed")
	}
	row, err := s.queries.UpdateStaticCredential(ctx, sqlc.UpdateStaticCredentialParams{
		TenantID: tenant, VaultID: vault, ID: id, McpServerUrl: current.MCPServerURL, TokenCiphertext: ciphertext,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, errors.New("credential update failed")
	}
	return credentialFromRow(sqlc.GetCredentialRow(row)), nil
}
