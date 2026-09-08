package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

func TestAcceptInvitationPreservesExistingAccount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invite := func(workspace, email, hash string) AcceptInvitationInput {
		t.Helper()
		token := sha256.Sum256([]byte(newID()))
		in := AcceptInvitationInput{TokenHash: token[:], Email: email, Role: "member", WorkspaceID: workspace, PasswordHash: hash, Now: now}
		err := st.CreateInvitation(ctx, CreateInvitationInput{ID: newID(), TokenHash: in.TokenHash, WorkspaceID: workspace, Email: email, Role: in.Role, InvitedBy: ids.UserID, ExpiresAt: now.Add(time.Hour), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	first := invite(ids.WorkspaceID, "invitee@example.com", "original-hash")
	created, err := st.AcceptInvitation(ctx, first)
	if err != nil || !created.UserCreated {
		t.Fatalf("new account: %+v, %v", created, err)
	}
	target, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Invitation target", CreatedBy: ids.UserID, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	second := invite(target.Workspace.ID, first.Email, "replacement-hash")
	q := sqlc.New(db)
	assertUnchanged := func() {
		t.Helper()
		row, err := q.GetPasswordHashByEmail(ctx, first.Email)
		if err != nil || row.PasswordHash != first.PasswordHash {
			t.Fatalf("password changed: %v", err)
		}
	}
	for _, actor := range []string{"", ids.UserID} {
		second.ActorUserID = actor
		if _, err := st.AcceptInvitation(ctx, second); !errors.Is(err, ErrInvitationSignInRequired) {
			t.Fatalf("actor %q: %v", actor, err)
		}
		pending, err := st.GetInvitationByTokenHash(ctx, second.TokenHash)
		if err != nil || pending.AcceptedAt != nil {
			t.Fatalf("rejected invitation consumed: %+v, %v", pending, err)
		}
		var count int
		if err := db.QueryRow(ctx, `select count(*) from workspace_members where workspace_id=$1 and user_id=$2`, second.WorkspaceID, created.Member.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unauthorized membership: %d, %v", count, err)
		}
		assertUnchanged()
	}
	second.ActorUserID = created.Member.UserID
	accepted, err := st.AcceptInvitation(ctx, second)
	if err != nil || accepted.UserCreated || accepted.Member.UserID != created.Member.UserID {
		t.Fatalf("authenticated acceptance: %+v, %v", accepted, err)
	}
	assertUnchanged()
	if _, err := st.AcceptInvitation(ctx, second); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("replay: %v", err)
	}

	third := invite(target.Workspace.ID, "missing-password@example.com", "")
	third.ActorUserID = ids.UserID
	if _, err := st.AcceptInvitation(ctx, third); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing new-account password: %v", err)
	}
	third.PasswordHash = "new-account-hash"
	if result, err := st.AcceptInvitation(ctx, third); err != nil || !result.UserCreated {
		t.Fatalf("new account retry: %+v, %v", result, err)
	}
}
