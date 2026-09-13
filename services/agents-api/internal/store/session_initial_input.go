package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// The Session upsert locks retries. Only the new row admits initial work, so a
// retry after completion or later Turns cannot submit the original input again.
func (s *Store) createSessionWithInputs(ctx context.Context, tenant string, params sqlc.CreateSessionParams, inputs []Input) (sqlc.Session, error) {
	var row sqlc.Session
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		row, err = q.CreateSession(ctx, params)
		if err != nil || row.ID != params.ID {
			return err
		}
		// Creation retries use the Session request hash. Keep the internal input key
		// independent of caller-supplied keys at the events endpoint.
		key := uuid.NewString()
		for position, input := range inputs {
			if _, err := admitInput(ctx, q, tenant, row.ID, key, int32(position), input); err != nil {
				return err
			}
		}
		return q.PruneSessionEvents(ctx, row.ID)
	})
	return row, err
}
