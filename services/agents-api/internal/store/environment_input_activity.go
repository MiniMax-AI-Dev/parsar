package store

import (
	"context"
	"errors"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentInputActivity is the reservation-owned override before a newer Turn exists.
type EnvironmentInputActivity struct {
	Status        string    `json:"status"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	LastActiveAt  time.Time `json:"last_active_at"`
}

func environmentInputActivity(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (*EnvironmentInputActivity, error) {
	row, err := q.GetEnvironmentInputActivity(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	activity := &EnvironmentInputActivity{Status: "idle", LastActiveAt: row.CreatedAt.Time}
	if row.SettledAt.Valid {
		activity.LastActiveAt = row.SettledAt.Time
	}
	if row.State == EnvironmentInputPending && row.ConnectionStatus != "connected" {
		activity.Status = "requires_action"
		activity.EnvironmentID = uuid.UUID(row.EnvironmentID.Bytes).String()
	}
	return activity, nil
}

func withEnvironmentInputActivity(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, apply func() error) error {
	before, err := environmentInputActivity(ctx, q, session)
	if err != nil {
		return err
	}
	if err := apply(); err != nil {
		return err
	}
	after, err := environmentInputActivity(ctx, q, session)
	if err != nil || after == nil {
		// Admitted input is represented by the normal Turn and Session events.
		return err
	}
	if before != nil && before.Status == after.Status && before.EnvironmentID == after.EnvironmentID {
		return nil
	}
	usage, err := q.SessionTokenUsage(ctx, session)
	if err != nil {
		return err
	}
	return recordSessionChange(ctx, q, session, SessionChange{
		Event:                    v1.SessionEvent{Type: "agent.session." + after.Status},
		EnvironmentInputActivity: after, SessionUsage: usage,
	})
}

func (s *Store) withEnvironmentInputSession(ctx context.Context, tenant, session string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		return withEnvironmentInputActivity(ctx, q, id, func() error { return apply(ctx, q, id) })
	})
}
