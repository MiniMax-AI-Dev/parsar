package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteCredential removes the stored secret without reading or decrypting it.
func (s *Store) DeleteCredential(ctx context.Context, tenantID, vaultID, credentialID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", ErrNotFound
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return "", ErrNotFound
	}
	id, err := parseID(credentialID)
	if err != nil {
		return "", ErrNotFound
	}
	deleted, err := s.queries.DeleteCredential(ctx, sqlc.DeleteCredentialParams{TenantID: tenant, VaultID: vault, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("credential deletion failed")
	}
	return uuid.UUID(deleted.Bytes).String(), nil
}
