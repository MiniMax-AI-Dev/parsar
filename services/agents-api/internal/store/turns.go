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

var ErrTurnConflict = errors.New("turn state changed or cancellation was requested")

const (
	TurnQueued     = "queued"
	TurnInProgress = "in_progress"
	TurnWaiting    = "waiting"
	TurnCompleted  = "completed"
	TurnFailed     = "failed"
	TurnCancelled  = "cancelled"
)

// Turn uses its Session's immutable execution configuration. Zero timestamps
// mean the corresponding event has not occurred. Outcome is adapter-owned data,
// not an upstream response; the API must project supported wire types explicitly.
type Turn struct {
	ID, SessionID, Status string
	CreatedAt             time.Time
	StartedAt             time.Time
	CompletedAt           time.Time
	CancelRequestedAt     time.Time
	Usage                 json.RawMessage
	Outcome               json.RawMessage
}

type TurnTransition struct {
	ExpectedStatus string
	Status         string
	Outcome        json.RawMessage
}

func (s *Store) GetTurn(ctx context.Context, tenantID, sessionID, turnID string) (Turn, error) {
	params, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return Turn{}, err
	}
	row, err := s.queries.GetTurn(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	if err != nil {
		return Turn{}, fmt.Errorf("get turn: %w", err)
	}
	return turnFromRow(row), nil
}

// TransitionTurn is a compare-and-set for execution callbacks. Once terminal,
// a Turn cannot be reopened or have its outcome overwritten, including by retries.
// A dispatcher must claim queued -> in_progress before sending work to a daemon.
func (s *Store) TransitionTurn(ctx context.Context, tenantID, sessionID, turnID string, input TurnTransition) (Turn, error) {
	params, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return Turn{}, err
	}
	if !validTransition(input.ExpectedStatus, input.Status) || len(input.Outcome) > 512*1024 {
		return Turn{}, fmt.Errorf("%w: invalid turn transition or outcome size", ErrInvalidInput)
	}
	outcome, err := canonicalJSONObject(input.Outcome)
	if err != nil {
		return Turn{}, err
	}
	if !terminalStatus(input.Status) && string(outcome) != "{}" {
		return Turn{}, fmt.Errorf("%w: outcome requires a terminal status", ErrInvalidInput)
	}
	var row sqlc.Turn
	err = s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, _ pgtype.UUID) error {
		if _, err := q.GetTurn(ctx, params); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var err error
		row, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{
			ID: params.ID, SessionID: params.SessionID, ExpectedStatus: input.ExpectedStatus,
			NewStatus: input.Status, Outcome: outcome,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTurnConflict
		}
		if err == nil && terminalStatus(row.Status) {
			if err = projectSource(ctx, q, row.SessionID, row.ID, "execution_"+row.Status, 0, row.Outcome, row.CompletedAt); err != nil {
				return err
			}
			row, err = q.GetTurn(ctx, params)
		}
		if err != nil {
			return err
		}
		return recordTurnChange(ctx, q, row, false)
	})
	if err != nil {
		return Turn{}, fmt.Errorf("transition turn: %w", err)
	}
	return turnFromRow(row), nil
}

func validTransition(from, to string) bool {
	switch from {
	case TurnQueued:
		return to == TurnInProgress || to == TurnFailed || to == TurnCancelled
	case TurnInProgress:
		return to == TurnWaiting || terminalStatus(to)
	case TurnWaiting:
		return to == TurnInProgress || terminalStatus(to)
	default:
		return false
	}
}

func terminalStatus(status string) bool {
	return status == TurnCompleted || status == TurnFailed || status == TurnCancelled
}

func turnLookup(tenantID, sessionID, turnID string) (sqlc.GetTurnParams, error) {
	var p sqlc.GetTurnParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	if p.SessionID, err = parseID(sessionID); err != nil {
		return p, err
	}
	p.ID, err = parseID(turnID)
	return p, err
}

func turnFromRow(row sqlc.Turn) Turn {
	return Turn{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), Status: row.Status,
		CreatedAt: row.CreatedAt.Time, StartedAt: row.StartedAt.Time, CompletedAt: row.CompletedAt.Time,
		CancelRequestedAt: row.CancelRequestedAt.Time, Outcome: json.RawMessage(row.Outcome), Usage: json.RawMessage(row.TokenUsage),
	}
}
