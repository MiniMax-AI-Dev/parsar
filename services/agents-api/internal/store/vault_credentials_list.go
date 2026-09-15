package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type CredentialPage struct {
	Credentials []Credential
	NextCursor  string
}

func (s *Store) ListCredentials(ctx context.Context, tenantID, vaultID, cursor string, limit int, ascending bool, statuses []string) (CredentialPage, error) {
	// An inaccessible parent is not an authorized empty collection.
	vault, err := s.GetVault(ctx, tenantID, vaultID)
	if err != nil {
		return CredentialPage{}, err
	}
	if limit < 1 || limit > 100 {
		return CredentialPage{}, fmt.Errorf("%w: internal page size must be 1..100", ErrInvalidInput)
	}
	if len(statuses) == 0 {
		statuses = []string{"active", "archived"}
	}
	for _, status := range statuses {
		if status != "active" && status != "archived" {
			return CredentialPage{}, fmt.Errorf("%w: invalid Credential status", ErrInvalidInput)
		}
	}
	tenant, _ := parseID(vault.TenantID)
	parent, _ := parseID(vault.ID)
	params := sqlc.ListCredentialsParams{TenantID: tenant, VaultID: parent, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending, Statuses: statuses}
	if cursor != "" {
		after, err := s.GetCredential(ctx, tenantID, vaultID, cursor)
		if err != nil {
			return CredentialPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListCredentials(ctx, params)
	if err != nil {
		return CredentialPage{}, fmt.Errorf("list credentials: %w", err)
	}
	page := CredentialPage{Credentials: make([]Credential, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Credentials = append(page.Credentials, credentialFromRow(sqlc.GetCredentialRow(row)))
	}
	return page, nil
}
