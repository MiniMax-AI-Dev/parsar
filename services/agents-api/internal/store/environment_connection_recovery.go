package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReconcileEnvironmentConnections runs before the new owner's connection producers start.
// It discards previous process generations and records loss of their connected transport.
func (s *Store) ReconcileEnvironmentConnections(ctx context.Context) error {
	if s.executionLease == nil {
		return errors.New("Environment reconciliation requires an execution lease")
	}
	after := pgtype.UUID{Valid: true}
	for {
		var rows []sqlc.ListEnvironmentConnectionsRow
		queryCtx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
		err := s.executionLease.withConn(queryCtx, func(conn *pgxpool.Conn) error {
			var err error
			rows, err = sqlc.New(conn).ListEnvironmentConnections(queryCtx, after)
			return err
		})
		cancel()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			err := s.withEnvironmentConnection(ctx, uuid.UUID(row.TenantID.Bytes).String(), uuid.UUID(row.ID.Bytes).String(), func(ctx context.Context, q *sqlc.Queries, current sqlc.GetSessionEnvironmentRow) error {
				if err := q.DeleteEnvironmentConnection(ctx, current.Environment.ID); err != nil {
					return err
				}
				if current.Environment.Status == "connected" {
					return recordEnvironmentConnection(ctx, q, current, "disconnected")
				}
				return nil
			})
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			after = row.ID
		}
	}
}
