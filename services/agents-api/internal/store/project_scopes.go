package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrProjectScopeConflict = errors.New("configured project conflicts with a persisted execution scope")

// EnsureProjectScopes binds or verifies the complete startup configuration atomically.
// Removing a caller key never removes or changes a persisted project association.
func (s *Store) EnsureProjectScopes(ctx context.Context, scopes []identity.ProjectScope) error {
	validated, err := identity.ProjectScopes(scopes)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		for _, scope := range validated {
			tenant, err := parseID(scope.TenantID)
			if err != nil {
				return err
			}
			_, err = q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: tenant, OrganizationID: scope.OrganizationID, ProjectID: scope.ProjectID})
			var databaseError *pgconn.PgError
			if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &databaseError) && databaseError.Code == "23505") {
				return ErrProjectScopeConflict
			}
			if err != nil {
				return fmt.Errorf("verify execution project scope: %w", err)
			}
		}
		return nil
	})
}
