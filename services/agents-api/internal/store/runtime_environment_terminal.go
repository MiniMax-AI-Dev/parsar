package store

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// terminateRuntimeEnvironment participates in the allocation's Session transaction.
// Public expiry does not assert compute removal or invent an expired SSE variant.
func terminateRuntimeEnvironment(ctx context.Context, q *sqlc.Queries, current sqlc.GetRuntimeAllocationRow) error {
	row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: current.TenantID, ID: current.SessionID})
	if err != nil {
		return err
	}
	if row.Environment.Status != "expired" && row.Environment.Status != "failed" {
		if current.Expired {
			err = q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: row.Environment.ID, Status: "expired"})
		} else {
			err = recordEnvironmentConnection(ctx, q, row, "failed")
		}
		if err != nil {
			return err
		}
	}
	return q.FailSessionEnvironmentInput(ctx, current.SessionID)
}
