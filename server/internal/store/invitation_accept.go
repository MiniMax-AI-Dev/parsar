package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

var ErrInvitationSignInRequired = errors.New("sign in as the invited account before accepting this invitation")

// AcceptInvitationInput identifies the invitation and authenticated actor, if any.
type AcceptInvitationInput struct {
	TokenHash    []byte
	Email        string
	Role         string
	WorkspaceID  string
	PasswordHash string
	ActorUserID  string
	Now          time.Time
}

// AcceptInvitation atomically consumes an invitation and adds its recipient to the workspace.
func (s *Store) AcceptInvitation(ctx context.Context, input AcceptInvitationInput) (AddWorkspaceMemberResult, error) {
	if !IsValidMemberRole(input.Role) {
		return AddWorkspaceMemberResult{}, fmt.Errorf("%w: %s", ErrInvalidMemberRole, input.Role)
	}
	email := normalizeEmail(input.Email)
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	beginner, ok := s.db.(txBeginner)
	if !ok {
		return AddWorkspaceMemberResult{}, fmt.Errorf("backing pool does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New(tx)

	// CAS: mark invitation consumed. Returns 0 rows if already used/revoked/expired.
	rows, err := q.AcceptWorkspaceInvitation(ctx, sqlc.AcceptWorkspaceInvitationParams{
		TokenHash: input.TokenHash,
		Now:       timestamptz(input.Now),
	})
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}
	if rows == 0 {
		return AddWorkspaceMemberResult{}, ErrInvitationInvalid
	}

	// Upsert user by email.
	name := email
	if at := strings.Index(email, "@"); at > 0 {
		name = email[:at]
	}
	userRow, err := q.UpsertUserByEmail(ctx, sqlc.UpsertUserByEmailParams{
		ID:    mustUUID(newID()),
		Email: email,
		Name:  name,
		Now:   timestamptz(input.Now),
	})
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	if !userRow.Created && (input.ActorUserID != userRow.ID || userRow.Status != "active") {
		return AddWorkspaceMemberResult{}, ErrInvitationSignInRequired
	}
	if userRow.Created {
		if input.PasswordHash == "" {
			return AddWorkspaceMemberResult{}, fmt.Errorf("%w: password is required for a new account", ErrInvalidInput)
		}
		metaBytes, err := json.Marshal(map[string]string{
			"password_hash": input.PasswordHash,
			"hashed_at":     input.Now.Format(time.RFC3339),
			"invited":       "true",
		})
		if err != nil {
			return AddWorkspaceMemberResult{}, fmt.Errorf("marshal identity metadata: %w", err)
		}
		if err := q.UpsertEmailPasswordIdentity(ctx, sqlc.UpsertEmailPasswordIdentityParams{
			ID:       mustUUID(newID()),
			UserID:   mustUUID(userRow.ID),
			Email:    email,
			Metadata: metaBytes,
			Now:      timestamptz(input.Now),
		}); err != nil {
			return AddWorkspaceMemberResult{}, fmt.Errorf("upsert email identity: %w", err)
		}
	}

	// Add workspace membership.
	memberRow, err := q.AddWorkspaceMember(ctx, sqlc.AddWorkspaceMemberParams{
		ID:            mustUUID(newID()),
		WorkspaceID:   wsUUID,
		UserID:        mustUUID(userRow.ID),
		Role:          input.Role,
		Status:        memberStatusActive,
		RequestReason: "",
		Now:           timestamptz(input.Now),
	})
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	return AddWorkspaceMemberResult{
		Member: WorkspaceMemberRead{
			ID:          memberRow.ID,
			WorkspaceID: memberRow.WorkspaceID,
			UserID:      memberRow.UserID,
			Role:        memberRow.Role,
			UserEmail:   userRow.Email,
			UserName:    userRow.Name,
			UserStatus:  userRow.Status,
			CreatedAt:   pgTime(memberRow.CreatedAt),
			UpdatedAt:   pgTime(memberRow.UpdatedAt),
		},
		UserCreated: userRow.Created,
	}, nil
}
