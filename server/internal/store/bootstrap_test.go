package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// ProvisionFirstOwner tests use the real Postgres harness because the
// gate-on-existing-owners invariant must hold across a real tx with a
// UNIQUE constraint.

func TestProvisionFirstOwnerEmptyDB(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	out, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "Admin@Example.com",
		Name:          "First Admin",
		WorkspaceName: "Acme Corp",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("ProvisionFirstOwner: %v", err)
	}
	if out.UserID == "" || out.WorkspaceID == "" || out.MemberID == "" {
		t.Fatalf("expected populated IDs, got %+v", out)
	}
	if !out.UserCreated {
		t.Fatalf("UserCreated should be true on fresh DB")
	}
	if out.WorkspaceName != "Acme Corp" {
		t.Fatalf("workspace name lost: %q", out.WorkspaceName)
	}
	if !strings.HasPrefix(out.WorkspaceSlug, "workspace-") || len(out.WorkspaceSlug) != len("workspace-")+12 || strings.Trim(out.WorkspaceSlug[len("workspace-"):], "0123456789abcdef") != "" {
		t.Fatalf("expected auto-slug workspace-<12hex>, got %q", out.WorkspaceSlug)
	}
	settings, err := st.GetWorkspaceRuntimeSettings(ctx, out.WorkspaceID)
	if err != nil {
		t.Fatalf("runtime settings: %v", err)
	}
	if settings.WorkspaceID != out.WorkspaceID {
		t.Fatalf("workspace settings workspace_id = %q, want %q", settings.WorkspaceID, out.WorkspaceID)
	}
	if settings.RuntimeCredentialSecretID != "" {
		t.Fatalf("fresh workspace should have no credential, got %q", settings.RuntimeCredentialSecretID)
	}
	if settings.SandboxAgentCount != 0 {
		t.Fatalf("fresh workspace should have 0 sandbox agents, got %d", settings.SandboxAgentCount)
	}

	count, err := st.ActiveWorkspaceOwnerCount(ctx)
	if err != nil {
		t.Fatalf("ActiveWorkspaceOwnerCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active owner after bootstrap, got %d", count)
	}
}

func TestProvisionFirstOwnerClosesGate(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	if _, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "first@example.com",
		WorkspaceName: "First",
		Now:           now,
	}); err != nil {
		t.Fatalf("first call: %v", err)
	}

	_, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "second@example.com",
		WorkspaceName: "Second",
		Now:           now.Add(time.Minute),
	})
	if !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("expected ErrBootstrapClosed on second call, got %v", err)
	}
}

func TestProvisionFirstOwnerRejectsBadInput(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	cases := []struct {
		name string
		in   ProvisionFirstOwnerInput
	}{
		{"empty email", ProvisionFirstOwnerInput{Email: "  ", WorkspaceName: "x"}},
		{"no at sign", ProvisionFirstOwnerInput{Email: "no-at", WorkspaceName: "x"}},
		{"empty workspace name", ProvisionFirstOwnerInput{Email: "a@b.com", WorkspaceName: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.ProvisionFirstOwner(ctx, tc.in)
			if !errors.Is(err, ErrInvalidWorkspaceInput) {
				t.Fatalf("expected ErrInvalidWorkspaceInput, got %v", err)
			}
		})
	}
}

func TestProvisionFirstOwnerDefaultsNameFromEmail(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	out, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "alice@example.com",
		WorkspaceName: "Alice WS",
	})
	if err != nil {
		t.Fatalf("ProvisionFirstOwner: %v", err)
	}
	user, err := st.GetUserByID(ctx, out.UserID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if user.Name != "alice" {
		t.Fatalf("expected name defaulted to local-part 'alice', got %q", user.Name)
	}
}

func TestActiveWorkspaceOwnerCountOnEmptyDB(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	count, err := st.ActiveWorkspaceOwnerCount(ctx)
	if err != nil {
		t.Fatalf("ActiveWorkspaceOwnerCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 on empty DB, got %d", count)
	}
}

func TestActiveWorkspaceOwnerCountIgnoresArchivedWorkspaces(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	out, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "owner@example.com",
		WorkspaceName: "Soon To Archive",
	})
	if err != nil {
		t.Fatalf("ProvisionFirstOwner: %v", err)
	}
	// Archive the workspace: owner row stays 'owner' but the workspace
	// is soft-deleted, so the bootstrap gate should reopen for DR.
	if _, err := st.ArchiveWorkspace(ctx, ArchiveWorkspaceInput{
		WorkspaceID: out.WorkspaceID,
		ActorID:     out.UserID,
		Now:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ArchiveWorkspace: %v", err)
	}

	count, err := st.ActiveWorkspaceOwnerCount(ctx)
	if err != nil {
		t.Fatalf("ActiveWorkspaceOwnerCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 active owners after workspace archive, got %d", count)
	}

	if _, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "owner2@example.com",
		WorkspaceName: "Reborn",
	}); err != nil {
		t.Fatalf("ProvisionFirstOwner after archive: %v", err)
	}
}

// TestProvisionFirstOwnerConcurrent fires N goroutines at an empty DB
// and asserts exactly one succeeds. Without the advisory lock inside
// ProvisionFirstOwner this fails under READ COMMITTED: two goroutines
// can both observe count==0 and both insert workspaces.
func TestProvisionFirstOwnerConcurrent(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	results := make([]ProvisionFirstOwnerResult, goroutines)

	// Fire from a single starter so they actually contend.
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
				Email:         "owner" + string(rune('0'+i)) + "@example.com",
				WorkspaceName: "WS" + string(rune('0'+i)),
			})
			results[i] = res
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	var successes, closed int
	for i, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBootstrapClosed):
			closed++
		default:
			t.Fatalf("goroutine %d unexpected error: %v (result=%+v)", i, err, results[i])
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful bootstrap, got %d successes / %d closed", successes, closed)
	}
	if closed != goroutines-1 {
		t.Fatalf("expected %d closed errors, got %d (successes=%d)", goroutines-1, closed, successes)
	}

	count, err := st.ActiveWorkspaceOwnerCount(ctx)
	if err != nil {
		t.Fatalf("ActiveWorkspaceOwnerCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active owner after concurrent bootstrap, got %d", count)
	}
}

// TestProvisionFirstOwnerLowercasesEmail asserts that a mixed-case
// input is stored in the canonical folded form, so a subsequent
// POST /auth/login (which lowercases the query key) hits the row.
// Without normalization the login handler's ToLower would produce a
// query miss and lock the owner out.
func TestProvisionFirstOwnerLowercasesEmail(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()

	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	if _, err := st.ProvisionFirstOwner(ctx, ProvisionFirstOwnerInput{
		Email:         "  Admin@Example.COM  ",
		WorkspaceName: "Acme Corp",
		Now:           now,
	}); err != nil {
		t.Fatalf("ProvisionFirstOwner: %v", err)
	}

	q := sqlc.New(db)
	got, err := q.GetActiveUserIDByEmail(ctx, "admin@example.com")
	if err != nil {
		t.Fatalf("stored email should be lowercase; GetActiveUserIDByEmail: %v", err)
	}
	if got == "" {
		t.Fatalf("expected a user id for lowercased email, got empty")
	}
	// The mixed-case form must NOT match — proves normalization is
	// one-sided (write folded, read must fold too).
	if _, err := q.GetActiveUserIDByEmail(ctx, "Admin@Example.COM"); err == nil {
		t.Fatalf("mixed-case lookup should miss the folded row")
	}
}
