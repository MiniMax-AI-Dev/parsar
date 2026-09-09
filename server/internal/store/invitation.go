package store

import (
	"context"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type CreateInvitationInput struct {
	ID          string
	TokenHash   []byte
	WorkspaceID string
	Email       string
	Name        string
	Role        string
	InvitedBy   string
	ExpiresAt   time.Time
	Now         time.Time
}

type InvitationRead struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	InvitedBy     string     `json:"invited_by"`
	ExpiresAt     time.Time  `json:"expires_at"`
	AcceptedAt    *time.Time `json:"accepted_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	CreatedAt     time.Time  `json:"created_at"`
	WorkspaceName string     `json:"workspace_name"`
}

func (s *Store) CreateInvitation(ctx context.Context, input CreateInvitationInput) error {
	q := sqlc.New(s.db)
	return q.CreateWorkspaceInvitation(ctx, sqlc.CreateWorkspaceInvitationParams{
		ID:          mustUUID(input.ID),
		TokenHash:   input.TokenHash,
		WorkspaceID: mustUUID(input.WorkspaceID),
		Email:       normalizeEmail(input.Email),
		Name:        strings.TrimSpace(input.Name),
		Role:        input.Role,
		InvitedBy:   mustUUID(input.InvitedBy),
		ExpiresAt:   timestamptz(input.ExpiresAt),
		CreatedAt:   timestamptz(input.Now),
	})
}

func (s *Store) GetInvitationByTokenHash(ctx context.Context, tokenHash []byte) (InvitationRead, error) {
	q := sqlc.New(s.db)
	row, err := q.GetWorkspaceInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return InvitationRead{}, err
	}
	inv := InvitationRead{
		ID:            row.ID,
		WorkspaceID:   row.WorkspaceID,
		Email:         row.Email,
		Name:          row.Name,
		Role:          row.Role,
		InvitedBy:     row.InvitedBy,
		ExpiresAt:     row.ExpiresAt.Time,
		CreatedAt:     row.CreatedAt.Time,
		WorkspaceName: row.WorkspaceName,
	}
	if row.AcceptedAt.Valid {
		inv.AcceptedAt = &row.AcceptedAt.Time
	}
	if row.RevokedAt.Valid {
		inv.RevokedAt = &row.RevokedAt.Time
	}
	return inv, nil
}
