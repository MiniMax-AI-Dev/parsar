package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) requireInitializedEnvironment(ctx context.Context, tenant, session pgtype.UUID) error {
	ready, err := s.queries.GetSessionInitializationReady(ctx, sqlc.GetSessionInitializationReadyParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !ready.Valid || !ready.Bool {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClaimRuntimeInitialization(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, true, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		return q.ClaimRuntimeInitialization(ctx, row.ID)
	})
}

func (s *Store) CompleteRuntimeInitialization(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, true, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		return q.CompleteRuntimeInitialization(ctx, row.ID)
	})
}
