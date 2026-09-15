package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteVault relies on the owning foreign key to remove every stored Credential.
func (s *Store) DeleteVault(ctx context.Context, tenantID, vaultID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", ErrNotFound
	}
	id, err := parseID(vaultID)
	if err != nil {
		return "", ErrNotFound
	}
	deleted, err := s.queries.DeleteVault(ctx, sqlc.DeleteVaultParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("vault deletion failed")
	}
	return uuid.UUID(deleted.Bytes).String(), nil
}
