package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type VaultPage struct {
	Vaults     []Vault
	NextCursor string
}

func (s *Store) ListVaults(ctx context.Context, tenantID, cursor string, limit int, ascending bool, statuses []string) (VaultPage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return VaultPage{}, err
	}
	if limit < 1 || limit > 100 {
		return VaultPage{}, fmt.Errorf("%w: internal page size must be 1..100", ErrInvalidInput)
	}
	if len(statuses) == 0 {
		statuses = []string{"active", "archived"}
	}
	for _, status := range statuses {
		if status != "active" && status != "archived" {
			return VaultPage{}, fmt.Errorf("%w: invalid Vault status", ErrInvalidInput)
		}
	}
	params := sqlc.ListVaultsParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending, Statuses: statuses}
	if cursor != "" {
		after, err := s.GetVault(ctx, tenantID, cursor)
		if err != nil {
			return VaultPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListVaults(ctx, params)
	if err != nil {
		return VaultPage{}, fmt.Errorf("list vaults: %w", err)
	}
	page := VaultPage{Vaults: make([]Vault, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		vault, err := vaultFromRow(row)
		if err != nil {
			return VaultPage{}, err
		}
		page.Vaults = append(page.Vaults, vault)
	}
	return page, nil
}
