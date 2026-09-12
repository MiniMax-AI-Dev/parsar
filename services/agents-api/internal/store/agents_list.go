package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type AgentPage struct {
	Agents     []SavedAgent
	NextCursor string
}

func (s *Store) ListAgents(ctx context.Context, tenantID, cursor string, limit int, ascending bool) (AgentPage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return AgentPage{}, err
	}
	if limit < 1 || limit > 100 {
		return AgentPage{}, fmt.Errorf("%w: internal page size must be 1..100", ErrInvalidInput)
	}
	params := sqlc.ListAgentsParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		after, err := s.GetAgent(ctx, tenantID, cursor)
		if err != nil {
			return AgentPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListAgents(ctx, params)
	if err != nil {
		return AgentPage{}, fmt.Errorf("list agents: %w", err)
	}
	page := AgentPage{Agents: make([]SavedAgent, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		agent, err := agentFromRow(row)
		if err != nil {
			return AgentPage{}, err
		}
		page.Agents = append(page.Agents, agent)
	}
	return page, nil
}
