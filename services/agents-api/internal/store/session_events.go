package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrStreamGap = errors.New("live event buffer exceeded; recover through Session and Items reads")

// SessionChange keeps transition snapshots separate from public response rendering.
type SessionChange struct {
	Sequence        int64                   `json:"-"`
	Event           v1.SessionEvent         `json:"event"`
	Turn            *Turn                   `json:"turn,omitempty"`
	SessionUsage    json.RawMessage         `json:"session_usage,omitempty"`
	RequiredActions []v1.FunctionCallAction `json:"required_actions,omitempty"`
}

func recordSessionChange(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, change SessionChange) error {
	change.Event.EventID = uuid.NewString()
	change.Event.SessionID = uuid.UUID(session.Bytes).String()
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	return q.AppendSessionEvent(ctx, sqlc.AppendSessionEventParams{ID: session, Payload: payload})
}

func recordTurnChange(ctx context.Context, q *sqlc.Queries, row sqlc.Turn, created bool) error {
	if row.Status == TurnWaiting {
		return nil
	}
	if terminalStatus(row.Status) {
		unfinished, err := q.FinishSessionItems(ctx, sqlc.FinishSessionItemsParams{SessionID: row.SessionID, TurnID: row.ID})
		if err != nil {
			return err
		}
		sort.Slice(unfinished, func(i, j int) bool { return unfinished[i].Position < unfinished[j].Position })
		for _, item := range unfinished {
			var value v1.Item
			if err := json.Unmarshal(item.Payload, &value); err != nil {
				return err
			}
			previous := value
			previous.Status = "in_progress"
			if err := recordItemChange(ctx, q, row.SessionID, item.OutputIndex, previous, value, nil); err != nil {
				return err
			}
		}
	}
	turn := turnFromRow(row)
	turn.Outcome = nil
	kind := row.Status
	if created {
		kind = "created"
	}
	change := SessionChange{Event: v1.SessionEvent{Type: "agent.session.turn." + kind, TurnID: turn.ID}, Turn: &turn}
	if err := recordSessionChange(ctx, q, row.SessionID, change); err != nil {
		return err
	}
	if !created && !terminalStatus(row.Status) {
		return nil
	}
	return recordSessionActivity(ctx, q, row, nil)
}

func (s *Store) SessionEventCursor(ctx context.Context, tenantID, sessionID string) (int64, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return 0, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return 0, err
	}
	cursor, err := s.queries.SessionEventCursor(ctx, sqlc.SessionEventCursorParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return cursor, err
}

func (s *Store) ListSessionEvents(ctx context.Context, tenantID, sessionID string, after int64) ([]SessionChange, error) {
	if after < 0 {
		return nil, ErrInvalidInput
	}
	latest, err := s.SessionEventCursor(ctx, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSessionEvents(ctx, sqlc.ListSessionEventsParams{TenantID: tenant, SessionID: id, Sequence: after})
	if err != nil {
		return nil, err
	}
	changes := make([]SessionChange, 0, len(rows))
	if len(rows) == 0 && latest > after {
		return nil, ErrStreamGap
	}
	for _, row := range rows {
		if row.Sequence != after+1 {
			return nil, ErrStreamGap
		}
		var change SessionChange
		decoder := json.NewDecoder(bytes.NewReader(row.Payload))
		decoder.UseNumber()
		if err := decoder.Decode(&change); err != nil {
			return nil, err
		}
		change.Sequence = row.Sequence
		changes = append(changes, change)
		after = row.Sequence
	}
	return changes, nil
}
