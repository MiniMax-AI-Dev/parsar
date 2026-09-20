package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// RecordCoreUsage records measured terminal usage before acknowledging settlement.
// The run lock serializes this with completion; CreateUsageLog is idempotent per run.
func (s *Store) RecordCoreUsage(ctx context.Context, runID string, input UsageInput) error {
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, err := uuid(runID)
	if err != nil {
		return err
	}
	var workspaceID string
	if err := tx.QueryRow(ctx, `select workspace_id::text from agent_runs where id=$1 and connector_type='agents_api' for update`, id).Scan(&workspaceID); err != nil {
		return err
	}
	now := time.Now().UTC()
	usage := normalizeUsageLog(input, workspaceID, runID, now, "agents_api")
	raw, err := json.Marshal(usage.Raw)
	if err != nil {
		return err
	}
	if err := sqlc.New(tx).CreateUsageLog(ctx, sqlc.CreateUsageLogParams{
		ID: mustUUID(usage.ID), WorkspaceID: mustUUID(workspaceID), AgentRunID: id,
		Provider: usage.Provider, Model: usage.Model, InputTokens: usage.InputTokens,
		OutputTokens: usage.OutputTokens, CostUsd: numeric(usage.CostUSD), Raw: raw, Now: timestamptz(now),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
