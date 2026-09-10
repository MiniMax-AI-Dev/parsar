package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type AgentMCPToken struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AgentMCPIdentity struct {
	AgentID, UserID, WorkspaceID, Name, Description string
}

func (s *Store) PutAgentMCPToken(ctx context.Context, id AgentMCPIdentity, hash string, token AgentMCPToken) error {
	a, err := uuid(id.AgentID)
	if err != nil {
		return err
	}
	u, err := uuid(id.UserID)
	if err != nil {
		return err
	}
	err = sqlc.New(s.db).PutAgentMCPToken(ctx, sqlc.PutAgentMCPTokenParams{
		AgentID: a, UserID: u, TokenHash: hash,
		CreatedAt: timestamptz(token.CreatedAt), ExpiresAt: timestamptz(token.ExpiresAt),
	})
	if err == nil {
		s.recordAgentMCPTokenAudit(id, "agent.mcp_token_rotated")
	}
	return err
}

func (s *Store) GetAgentMCPToken(ctx context.Context, agentID, userID string) (*AgentMCPToken, error) {
	a, err := uuid(agentID)
	if err != nil {
		return nil, err
	}
	u, err := uuid(userID)
	if err != nil {
		return nil, err
	}
	row, err := sqlc.New(s.db).GetAgentMCPToken(ctx, sqlc.GetAgentMCPTokenParams{AgentID: a, UserID: u})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &AgentMCPToken{CreatedAt: pgTime(row.CreatedAt), ExpiresAt: pgTime(row.ExpiresAt)}, nil
}

func (s *Store) DeleteAgentMCPToken(ctx context.Context, id AgentMCPIdentity) error {
	a, err := uuid(id.AgentID)
	if err != nil {
		return err
	}
	u, err := uuid(id.UserID)
	if err != nil {
		return err
	}
	err = sqlc.New(s.db).DeleteAgentMCPToken(ctx, sqlc.DeleteAgentMCPTokenParams{AgentID: a, UserID: u})
	if err == nil {
		s.recordAgentMCPTokenAudit(id, "agent.mcp_token_revoked")
	}
	return err
}

func (s *Store) ResolveAgentMCPToken(ctx context.Context, hash string, now time.Time) (AgentMCPIdentity, bool, error) {
	row, err := sqlc.New(s.db).ResolveAgentMCPToken(ctx, sqlc.ResolveAgentMCPTokenParams{TokenHash: hash, ExpiresAt: timestamptz(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentMCPIdentity{}, false, nil
	}
	if err != nil {
		return AgentMCPIdentity{}, false, err
	}
	return AgentMCPIdentity{AgentID: row.TAgentID, UserID: row.TUserID, WorkspaceID: row.AWorkspaceID, Name: row.Name, Description: row.Description}, true, nil
}

func (s *Store) recordAgentMCPTokenAudit(id AgentMCPIdentity, event string) {
	s.emitAuditEvent(audit.Event{OccurredAt: time.Now().UTC(), Source: audit.SourceRuntime,
		EventType: event, ActorType: audit.ActorTypeUser, ActorID: id.UserID,
		TargetType: "agent", TargetID: id.AgentID, WorkspaceID: id.WorkspaceID})
}
