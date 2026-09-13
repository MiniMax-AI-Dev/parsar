package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeleteAgent removes a saved resource independently of execution snapshots.
func (s *Store) DeleteAgent(ctx context.Context, tenantID, agentID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", err
	}
	id, err := parseID(agentID)
	if err != nil {
		return "", err
	}
	deleted, err := s.queries.DeleteAgent(ctx, sqlc.DeleteAgentParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete agent: %w", err)
	}
	return uuid.UUID(deleted.Bytes).String(), nil
}
