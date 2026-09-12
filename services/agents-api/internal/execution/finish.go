package execution

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func finishDelivery(ctx context.Context, journal *journal, result *Result, cancelReply <-chan cancellationResult, pendingInput bool, functions *functionExchange) string {
	if cancelReply != nil {
		select {
		case reply := <-cancelReply:
			if recordCancellation(ctx, journal, reply, result) != nil {
				result.ErrorCode = "event_persistence_failed"
				return store.TurnFailed
			}
			if reply.err == nil && reply.ack.Applied {
				return store.TurnCancelled
			}
			if reply.err != nil || reply.ack.ErrorCode != "run_inactive" {
				result.ErrorCode = "cancel_unconfirmed"
				return store.TurnFailed
			}
		case <-ctx.Done():
			result.ErrorCode = "cancel_unconfirmed"
			return store.TurnFailed
		}
	}
	if result.ErrorCode == "" {
		if pendingInput {
			result.ErrorCode = "input_outcome_unknown"
		} else if functions.complete(ctx) != nil {
			result.ErrorCode = "function_result_unconfirmed"
		} else {
			return store.TurnCompleted
		}
	}
	return store.TurnFailed
}
