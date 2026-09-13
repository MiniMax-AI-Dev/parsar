package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// UpdateAgentInput contains validated top-level replacements, not a full snapshot.
// A nil metadata pointer preserves the existing map; a supplied map replaces it.
type UpdateAgentInput struct {
	Configuration json.RawMessage
	Metadata      *map[string]string
}

func (s *Store) UpdateAgent(ctx context.Context, tenantID, agentID string, input UpdateAgentInput) (SavedAgent, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SavedAgent{}, err
	}
	id, err := parseID(agentID)
	if err != nil {
		return SavedAgent{}, err
	}
	raw := input.Configuration
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > 512*1024 {
		return SavedAgent{}, ErrInvalidInput
	}
	raw, err = canonicalJSONObject(raw)
	if err != nil {
		return SavedAgent{}, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return SavedAgent{}, err
	}
	var metadata []byte
	if input.Metadata != nil {
		metadata, err = encodeMetadata(*input.Metadata)
		if err != nil {
			return SavedAgent{}, err
		}
	}
	if len(patch) == 0 && input.Metadata == nil {
		return s.GetAgent(ctx, tenantID, agentID)
	}
	var updated SavedAgent
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.LockAgent(ctx, sqlc.LockAgentParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var configuration map[string]json.RawMessage
		if err := json.Unmarshal(row.Configuration, &configuration); err != nil {
			return err
		}
		for field, value := range patch {
			configuration[field] = value
		}
		merged, err := json.Marshal(configuration)
		if err != nil {
			return err
		}
		merged, err = canonicalJSONObject(merged)
		if err != nil {
			return err
		}
		if len(merged) > 512*1024 {
			return ErrInvalidInput
		}
		if input.Metadata == nil {
			metadata = row.Metadata
		}
		row, err = q.UpdateAgent(ctx, sqlc.UpdateAgentParams{TenantID: tenant, ID: id, Configuration: merged, Metadata: metadata})
		if err != nil {
			return err
		}
		updated, err = agentFromRow(row)
		return err
	})
	if err != nil {
		return SavedAgent{}, fmt.Errorf("update agent: %w", err)
	}
	return updated, nil
}
