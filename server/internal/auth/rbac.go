package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

var ErrUnauthenticated = errors.New("auth: unauthenticated")
var ErrForbidden = errors.New("auth: forbidden")
var ErrNotMember = store.ErrNotMember

type RoleStore interface {
	GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error)
}

func RequireWorkspaceRole(ctx context.Context, roleStore RoleStore, workspaceID string, allowed ...string) error {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return ErrUnauthenticated
	}
	if IsPlatformAdmin(userID) {
		return nil
	}
	role, err := roleStore.GetWorkspaceMemberRole(ctx, workspaceID, userID)
	if err != nil {
		if errors.Is(err, ErrNotMember) {
			return err
		}
		return fmt.Errorf("require workspace role: %w", err)
	}
	if !roleAllowed(role, allowed) {
		return ErrForbidden
	}
	return nil
}

func roleAllowed(role string, allowed []string) bool {
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}
