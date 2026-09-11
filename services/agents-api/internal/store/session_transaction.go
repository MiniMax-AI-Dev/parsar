package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// All Turn admission and lifecycle writes lock the tenant-scoped Session first.
// This orders inputs against completion/cancellation across service processes.
func (s *Store) withSession(ctx context.Context, tenantID, sessionID string, apply func(*sqlc.Queries, pgtype.UUID) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.LockSession(ctx, sqlc.LockSessionParams{TenantID: tenant, ID: id}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := ensureSessionItems(ctx, q, id); err != nil {
			return err
		}
		if err := apply(q, id); err != nil {
			return err
		}
		return q.PruneSessionEvents(ctx, id)
	})
}
