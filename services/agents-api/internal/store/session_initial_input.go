package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

func validateInitialInputs(inputs []Input) ([]Input, json.RawMessage, error) {
	for _, input := range inputs {
		if input.Kind != "message" {
			return nil, nil, ErrInvalidInput
		}
	}
	return validateInputs(inputs)
}

// The Session upsert locks retries. Only the new row reserves or admits work, so a
// retry after completion or later Turns cannot submit the original input again.
func (s *Store) createSessionResources(ctx context.Context, tenant string, params sqlc.CreateSessionParams, inputs []Input, encodedInput json.RawMessage) (sqlc.Session, *Environment, error) {
	var row sqlc.Session
	var environment *Environment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.CreateSession(ctx, params)
		if err != nil {
			return err
		}
		if row.ID == params.ID {
			if err := createSessionEnvironment(ctx, q, row); err != nil {
				return err
			}
		}
		environment, err = sessionEnvironmentSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		if row.ID != params.ID || len(inputs) == 0 {
			return nil
		}
		// Creation retries use the Session request hash. Keep the internal input key
		// independent of caller-supplied keys at the events endpoint.
		key := uuid.NewString()
		if environment != nil {
			// Environment input waits for the existing preparation and leased promotion.
			if err := withEnvironmentInputActivity(ctx, q, row.ID, func() error {
				_, err := q.CreateEnvironmentInputReservation(ctx, sqlc.CreateEnvironmentInputReservationParams{
					ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: row.ID,
					IdempotencyKey: key, Batch: encodedInput, IsInitial: true,
				})
				return err
			}); err != nil {
				return err
			}
		} else {
			for position, input := range inputs {
				if _, err := admitInput(ctx, q, tenant, row.ID, key, int32(position), input); err != nil {
					return err
				}
			}
		}
		return q.PruneSessionEvents(ctx, row.ID)
	})
	return row, environment, err
}
