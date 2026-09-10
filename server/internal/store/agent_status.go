package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) DisableAgent(ctx context.Context, agentID, actorID string) (AgentStatusRead, error) {
	return s.setAgentStatus(ctx, agentID, actorID, "disabled", auditAgentDisabled)
}

func (s *Store) EnableAgent(ctx context.Context, agentID, actorID string) (AgentStatusRead, error) {
	return s.setAgentStatus(ctx, agentID, actorID, "active", auditAgentEnabled)
}

func (s *Store) setAgentStatus(ctx context.Context, agentID, actorID, targetStatus, eventType string) (AgentStatusRead, error) {
	now := time.Now().UTC()
	aUUID, err := uuid(agentID)
	if err != nil {
		return AgentStatusRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return AgentStatusRead{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	detail, err := queries.GetAgentDetailForRead(ctx, aUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return AgentStatusRead{}, err
	}

	var (
		updatedID, updatedWS, updatedStatus string
		updatedConfig                       []byte
		updatedCreatedAt, updatedUpdatedAt  pgtype.Timestamptz
	)
	switch targetStatus {
	case "disabled":
		row, err := queries.DisableAgent(ctx, sqlc.DisableAgentParams{ID: aUUID, Now: timestamptz(now)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
			}
			return AgentStatusRead{}, err
		}
		updatedID, updatedWS, updatedStatus = row.ID, row.WorkspaceID, row.Status
		updatedConfig, updatedCreatedAt, updatedUpdatedAt = row.Config, row.CreatedAt, row.UpdatedAt
	case "active":
		row, err := queries.EnableAgent(ctx, sqlc.EnableAgentParams{ID: aUUID, Now: timestamptz(now)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
			}
			return AgentStatusRead{}, err
		}
		updatedID, updatedWS, updatedStatus = row.ID, row.WorkspaceID, row.Status
		updatedConfig, updatedCreatedAt, updatedUpdatedAt = row.Config, row.CreatedAt, row.UpdatedAt
	default:
		return AgentStatusRead{}, fmt.Errorf("invalid agent target status: %s", targetStatus)
	}

	if err := tx.Commit(ctx); err != nil {
		return AgentStatusRead{}, err
	}

	s.emitAgentAudit(now, actorID, eventType, "agent", updatedID, updatedWS, map[string]any{
		"agent_slug": detail.AgentSlug,
		"agent_name": detail.AgentName,
		"prev":       detail.Status,
		"next":       updatedStatus,
	})

	return AgentStatusRead{
		WorkspaceID:   updatedWS,
		AgentID:       updatedID,
		AgentName:     detail.AgentName,
		AgentSlug:     detail.AgentSlug,
		ConnectorType: detail.ConnectorType,
		Status:        updatedStatus,
		Config:        decodeJSONMap(updatedConfig),
		CreatedAt:     pgTime(updatedCreatedAt),
		UpdatedAt:     pgTime(updatedUpdatedAt),
	}, nil
}
