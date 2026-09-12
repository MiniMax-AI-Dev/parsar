package store

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
)

func functionActions(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) ([]v1.FunctionCallAction, error) {
	actions := make([]v1.FunctionCallAction, 0)
	if !acceptsFunctionResult(turn) {
		return actions, nil
	}
	calls, err := q.ListPendingFunctionCalls(ctx, sqlc.ListPendingFunctionCallsParams{SessionID: turn.SessionID, TurnID: turn.ID})
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		actions = append(actions, v1.FunctionCallAction{Type: "function_call", CallID: call.CallID, Name: call.Name, TurnID: uuid.UUID(turn.ID.Bytes).String(), Arguments: json.RawMessage(call.Arguments)})
	}
	return actions, nil
}

func recordFunctionState(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) error {
	actions, err := functionActions(ctx, q, turn)
	if err != nil {
		return err
	}
	status := TurnInProgress
	if len(actions) > 0 {
		status = TurnWaiting
	}
	if turn.Status != status {
		turn, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: turn.ID, SessionID: turn.SessionID, ExpectedStatus: turn.Status, NewStatus: status, Outcome: []byte(`{}`)})
		if err != nil {
			return err
		}
		if err := recordTurnChange(ctx, q, turn, false); err != nil {
			return err
		}
	}
	return recordSessionActivity(ctx, q, turn, actions)
}

func recordSessionActivity(ctx context.Context, q *sqlc.Queries, row sqlc.Turn, actions []v1.FunctionCallAction) error {
	turn := turnFromRow(row)
	turn.Outcome = nil
	status := "in_progress"
	if terminalStatus(row.Status) {
		status = "idle"
		if row.Status == TurnFailed {
			status = "failed"
		}
	} else if len(actions) > 0 {
		status = "requires_action"
	}
	usage, err := q.SessionTokenUsage(ctx, row.SessionID)
	if err != nil {
		return err
	}
	return recordSessionChange(ctx, q, row.SessionID, SessionChange{
		Event: v1.SessionEvent{Type: "agent.session." + status}, Turn: &turn,
		SessionUsage: usage, RequiredActions: actions,
	})
}
