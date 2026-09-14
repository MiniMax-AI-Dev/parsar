package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

const (
	EnvironmentInputPending   = "pending"
	EnvironmentInputAdmitted  = "admitted"
	EnvironmentInputExpired   = "expired"
	EnvironmentInputCancelled = "cancelled"
)

// EnvironmentInputReservation is private admission state, not a public Session projection.
type EnvironmentInputReservation struct {
	ID        string
	SessionID string
	State     string
	Inputs    []Input
	CreatedAt time.Time
	Deadline  time.Time
	SettledAt *time.Time
	Receipts  []InputReceipt
}

// ReserveEnvironmentInput retains one message batch without creating a Turn or history.
func (s *Store) ReserveEnvironmentInput(ctx context.Context, tenantID, sessionID, key string, inputs []Input) (EnvironmentInputReservation, error) {
	if err := validateInputKey(key); err != nil {
		return EnvironmentInputReservation{}, err
	}
	_, encoded, err := validateInitialInputs(inputs)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	var result EnvironmentInputReservation
	err = s.withEnvironmentInputSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.FindEnvironmentInputReservation(ctx, sqlc.FindEnvironmentInputReservationParams{
			SessionID: session, IdempotencyKey: key, Batch: encoded,
		})
		if err == nil {
			if !previous.Matches {
				return ErrIdempotencyConflict
			}
			result, err = settleEnvironmentInput(ctx, q, tenantID, previous.EnvironmentInputReservation, EnvironmentInputExpired)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		receipts, err := inputBatchReceipts(ctx, q, session, key, encoded)
		if err != nil {
			return err
		}
		if len(receipts) > 0 {
			// Earlier direct admission has receipts, but never had a reservation or deadline.
			result = EnvironmentInputReservation{SessionID: sessionID, State: EnvironmentInputAdmitted, Receipts: receipts}
			return nil
		}
		if _, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: session}); errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidInput
		} else if err != nil {
			return err
		}
		if err := environmentInputMayStart(ctx, q, session); err != nil {
			return err
		}
		if err := checkEnvironmentInputGate(ctx, q, session, key, encoded); err != nil {
			return err
		}
		row, err := q.CreateEnvironmentInputReservation(ctx, sqlc.CreateEnvironmentInputReservationParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session, IdempotencyKey: key, Batch: encoded,
		})
		if err != nil {
			return err
		}
		result, err = environmentInputFromRow(row)
		return err
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

func (s *Store) GetEnvironmentInputReservation(ctx context.Context, tenantID, sessionID, reservationID string) (EnvironmentInputReservation, error) {
	id, err := parseID(reservationID)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	var result EnvironmentInputReservation
	err = s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		row, err := q.GetEnvironmentInputReservation(ctx, sqlc.GetEnvironmentInputReservationParams{SessionID: session, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result, err = environmentInputOutcome(ctx, q, row)
		return err
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

// PromoteEnvironmentInput admits and claims work for the retained native preparation.
func (s *Store) PromoteEnvironmentInput(ctx context.Context, tenantID, sessionID, reservationID string) (EnvironmentInputReservation, error) {
	if s.executionLease == nil {
		return EnvironmentInputReservation{}, errors.New("Environment input promotion requires an execution lease")
	}
	return s.settleEnvironmentInput(ctx, tenantID, sessionID, reservationID, EnvironmentInputAdmitted)
}

func (s *Store) CancelEnvironmentInput(ctx context.Context, tenantID, sessionID, reservationID string) (EnvironmentInputReservation, error) {
	return s.settleEnvironmentInput(ctx, tenantID, sessionID, reservationID, EnvironmentInputCancelled)
}

func (s *Store) ExpireEnvironmentInput(ctx context.Context, tenantID, sessionID, reservationID string) (EnvironmentInputReservation, error) {
	return s.settleEnvironmentInput(ctx, tenantID, sessionID, reservationID, EnvironmentInputExpired)
}

func (s *Store) settleEnvironmentInput(ctx context.Context, tenantID, sessionID, reservationID, state string) (EnvironmentInputReservation, error) {
	id, err := parseID(reservationID)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	var result EnvironmentInputReservation
	err = s.withEnvironmentInputSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		row, err := q.GetEnvironmentInputReservation(ctx, sqlc.GetEnvironmentInputReservationParams{SessionID: session, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result, err = settleEnvironmentInput(ctx, q, tenantID, row, state)
		return err
	})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	return result, nil
}

func settleEnvironmentInput(ctx context.Context, q *sqlc.Queries, tenantID string, row sqlc.EnvironmentInputReservation, state string) (EnvironmentInputReservation, error) {
	if row.State != EnvironmentInputPending {
		return environmentInputOutcome(ctx, q, row)
	}
	if err := q.ExpireEnvironmentInputReservation(ctx, sqlc.ExpireEnvironmentInputReservationParams{SessionID: row.SessionID, ID: row.ID}); err != nil {
		return EnvironmentInputReservation{}, err
	}
	row, err := q.GetEnvironmentInputReservation(ctx, sqlc.GetEnvironmentInputReservationParams{SessionID: row.SessionID, ID: row.ID})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	// Terminal outcomes are successful storage results so settlement is not rolled back.
	if row.State != EnvironmentInputPending || state == EnvironmentInputExpired {
		return environmentInputOutcome(ctx, q, row)
	}
	result, err := environmentInputFromRow(row)
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	if state == EnvironmentInputAdmitted {
		if err := environmentInputMayStart(ctx, q, row.SessionID); err != nil {
			return EnvironmentInputReservation{}, err
		}
		for position, input := range result.Inputs {
			receipt, err := admitInput(ctx, q, tenantID, row.SessionID, row.IdempotencyKey, int32(position), input)
			if err != nil {
				return EnvironmentInputReservation{}, err
			}
			result.Receipts = append(result.Receipts, receipt)
		}
	}
	row, err = q.SettleEnvironmentInputReservation(ctx, sqlc.SettleEnvironmentInputReservationParams{SessionID: row.SessionID, ID: row.ID, State: state})
	if err != nil {
		return EnvironmentInputReservation{}, err
	}
	if state == EnvironmentInputAdmitted {
		params, err := turnLookup(tenantID, result.SessionID, result.Receipts[0].TurnID)
		if err != nil {
			return EnvironmentInputReservation{}, err
		}
		if _, err := transitionTurn(ctx, q, params, TurnTransition{
			ExpectedStatus: TurnQueued, Status: TurnInProgress, Outcome: json.RawMessage(`{}`),
		}); err != nil {
			return EnvironmentInputReservation{}, err
		}
	}
	result.State = row.State
	result.SettledAt = &row.SettledAt.Time
	return result, nil
}

func environmentInputMayStart(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	_, err := q.GetActiveTurn(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err == nil {
		return ErrTurnConflict
	}
	return err
}

func environmentInputOutcome(ctx context.Context, q *sqlc.Queries, row sqlc.EnvironmentInputReservation) (EnvironmentInputReservation, error) {
	result, err := environmentInputFromRow(row)
	if err != nil || row.State != EnvironmentInputAdmitted {
		return result, err
	}
	result.Receipts, err = inputBatchReceipts(ctx, q, row.SessionID, row.IdempotencyKey, row.Batch)
	if err == nil && len(result.Receipts) == 0 {
		err = errors.New("admitted Environment input has no receipts")
	}
	return result, err
}

func environmentInputFromRow(row sqlc.EnvironmentInputReservation) (EnvironmentInputReservation, error) {
	result := EnvironmentInputReservation{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		State: row.State, CreatedAt: row.CreatedAt.Time, Deadline: row.Deadline.Time,
	}
	if row.SettledAt.Valid {
		result.SettledAt = &row.SettledAt.Time
	}
	if err := json.Unmarshal(row.Batch, &result.Inputs); err != nil {
		return EnvironmentInputReservation{}, fmt.Errorf("decode Environment input: %w", err)
	}
	return result, nil
}

func checkEnvironmentInputGate(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, key string, batch json.RawMessage) error {
	gate, err := q.CheckEnvironmentInputGate(ctx, sqlc.CheckEnvironmentInputGateParams{SessionID: session, IdempotencyKey: key, Batch: batch})
	if err != nil {
		return err
	}
	if !gate.Matches {
		return ErrIdempotencyConflict
	}
	if gate.Blocked {
		return ErrTurnConflict
	}
	return nil
}
