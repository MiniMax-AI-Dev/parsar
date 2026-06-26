package auth

import (
	"context"
	"errors"
	"testing"
)

type stubRoleStore struct {
	wsRole  string
	wsErr   error
	wsCalls int
}

func (s *stubRoleStore) GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error) {
	s.wsCalls++
	return s.wsRole, s.wsErr
}

func TestRequireWorkspaceRoleAdminBypassesMembership(t *testing.T) {
	t.Cleanup(func() { SetPlatformAdminIDs(nil) })
	admin := "00000000-0000-0000-0000-0000000000ad"
	SetPlatformAdminIDs([]string{admin})

	store := &stubRoleStore{wsErr: ErrNotMember}
	ctx := WithUserID(context.Background(), admin)
	if err := RequireWorkspaceRole(ctx, store, "ws-1", "owner"); err != nil {
		t.Fatalf("platform admin should bypass workspace membership: %v", err)
	}
	if store.wsCalls != 0 {
		t.Fatalf("role store should not be hit for platform admin, got %d calls", store.wsCalls)
	}
}

func TestRequireWorkspaceRoleNonAdminStillChecked(t *testing.T) {
	t.Cleanup(func() { SetPlatformAdminIDs(nil) })
	SetPlatformAdminIDs([]string{"00000000-0000-0000-0000-0000000000ad"})

	store := &stubRoleStore{wsErr: ErrNotMember}
	ctx := WithUserID(context.Background(), "00000000-0000-0000-0000-000000000001")
	err := RequireWorkspaceRole(ctx, store, "ws-1", "owner")
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-admin should still hit the role store; got err=%v", err)
	}
	if store.wsCalls != 1 {
		t.Fatalf("role store should be hit once, got %d", store.wsCalls)
	}
}

func TestRequireWorkspaceRoleUnauthenticated(t *testing.T) {
	store := &stubRoleStore{}
	if err := RequireWorkspaceRole(context.Background(), store, "ws-1", "owner"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestIsPlatformAdminCaseInsensitive(t *testing.T) {
	t.Cleanup(func() { SetPlatformAdminIDs(nil) })
	SetPlatformAdminIDs([]string{"00000000-0000-0000-0000-0000000000AD"})
	if !IsPlatformAdmin("00000000-0000-0000-0000-0000000000ad") {
		t.Fatal("admin lookup should be case-insensitive")
	}
	if IsPlatformAdmin("") {
		t.Fatal("empty userID must never match")
	}
}

func TestSetPlatformAdminIDsEmptyDisables(t *testing.T) {
	SetPlatformAdminIDs([]string{"00000000-0000-0000-0000-0000000000ad"})
	SetPlatformAdminIDs(nil)
	if IsPlatformAdmin("00000000-0000-0000-0000-0000000000ad") {
		t.Fatal("nil allowlist should disable platform admin")
	}
}
