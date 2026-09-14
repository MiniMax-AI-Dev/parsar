package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func TestExecutorPrincipalBeforeSessionAndSharedLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	p := FixtureExecutorPrincipal(t, s, uuid.NewString())
	keyID := uuid.NewString()
	issued, err := s.IssueExecutorCredential(ctx, p, keyID, "")
	if err != nil || issued.KeyID != keyID || issued.EnvironmentID != "" || len(issued.Token) != 43 {
		t.Fatal("pre-Session principal issuance failed", err)
	}
	page, err := s.ListSessions(ctx, p.TenantID, "", 10, false, nil)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatal("issuance created a Session", err)
	}
	create := func(principal identity.Principal) (Session, Environment) {
		t.Helper()
		input := environmentInput(uuid.NewString(), "self_hosted", "/workspace")
		input.Creator = principal.Subject()
		session, err := s.CreateSession(ctx, principal.TenantID, input)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := s.GetSessionEnvironment(ctx, principal.TenantID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return session, environment
	}
	check := func(st *Store, environment string, key IssuedExecutorCredential, allowed bool) {
		t.Helper()
		owner, err := st.AuthenticateEnvironmentExecutor(ctx, environment, executorDigest(key.Token))
		if allowed && (err != nil || owner != p.TenantID) || !allowed && !errors.Is(err, ErrNotFound) {
			t.Fatal("unexpected principal authorization", allowed, err)
		}
	}
	first, one := create(p)
	_, two := create(p)
	check(s, one.ID, issued, true)
	check(s, two.ID, issued, true)
	check(s, uuid.NewString(), issued, false)
	otherKind, otherID := p, p
	otherKind.SubjectKind = "user"
	otherID.SubjectID = "other-subject"
	foreign := FixtureExecutorPrincipal(t, s, uuid.NewString())
	for _, different := range []identity.Principal{otherKind, otherID, foreign} {
		_, target := create(different)
		check(s, target.ID, issued, false)
		if _, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), target.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("restricted key accepted another creator/project", err)
		}
		if _, err := s.RotateExecutorCredential(ctx, different, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign rotation", err)
		}
		if err := s.RevokeExecutorCredential(ctx, different, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign revocation", err)
		}
	}
	for _, different := range []identity.Principal{
		{ProjectScope: identity.ProjectScope{TenantID: p.TenantID, OrganizationID: "other-org", ProjectID: p.ProjectID}, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID},
		{ProjectScope: identity.ProjectScope{TenantID: p.TenantID, OrganizationID: p.OrganizationID, ProjectID: "other-project"}, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID},
	} {
		if _, err := s.IssueExecutorCredential(ctx, different, uuid.NewString(), ""); !errors.Is(err, ErrNotFound) {
			t.Fatal("unverified scope issuance", err)
		}
		if _, err := s.RotateExecutorCredential(ctx, different, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("unverified scope rotation", err)
		}
		if err := s.RevokeExecutorCredential(ctx, different, keyID); !errors.Is(err, ErrNotFound) {
			t.Fatal("unverified scope revocation", err)
		}
	}
	restricted, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), one.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(s, one.ID, restricted, true)
	check(s, two.ID, restricted, false)
	// Reopening the database preserves both key identity and multi-Session authority.
	pool.Close()
	restarted, newPool := testStore(t)
	check(restarted, one.ID, issued, true)
	check(restarted, two.ID, issued, true)
	if err := restarted.DeleteSession(ctx, p.TenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	check(restarted, one.ID, issued, false)
	check(restarted, two.ID, issued, true)
	if err := restarted.RevokeExecutorCredential(ctx, p, restricted.KeyID); err != nil {
		t.Fatal("revoke deleted restriction", err)
	}
	rotated, err := restarted.RotateExecutorCredential(ctx, p, keyID)
	if err != nil || rotated.KeyID != keyID || rotated.EnvironmentID != "" || rotated.Token == issued.Token {
		t.Fatal("principal rotation", err)
	}
	check(restarted, two.ID, issued, false)
	check(restarted, two.ID, rotated, true)
	var retained bool
	if err := newPool.QueryRow(ctx, `SELECT tenant_id=$2 AND subject_kind=$3 AND subject_id=$4
		AND environment_id IS NULL AND created_at < issued_at AND revoked_at IS NULL
		FROM environment_executor_credentials WHERE key_id=$1`, keyID, p.TenantID, p.SubjectKind, p.SubjectID).Scan(&retained); err != nil || !retained {
		t.Fatal("rotation changed principal or creation identity", err)
	}
	if err := restarted.RevokeExecutorCredential(ctx, p, keyID); err != nil {
		t.Fatal(err)
	}
	check(restarted, two.ID, rotated, false)
	if _, err := restarted.IssueExecutorCredential(ctx, p, keyID, ""); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("issue restored revoked authority", err)
	}
	restored, err := restarted.RotateExecutorCredential(ctx, p, keyID)
	if err != nil {
		t.Fatal(err)
	}
	check(restarted, two.ID, restored, true)
}

func TestExecutorPrincipalRequiresVerifiedScopeAndRecordedCreator(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	p := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: uuid.NewString(), OrganizationID: "org", ProjectID: uuid.NewString()}, SubjectKind: "user", SubjectID: "owner"}
	if _, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("issuer manufactured a project mapping", err)
	}
	if err := s.EnsureProjectScopes(ctx, []identity.ProjectScope{p.ProjectScope}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []identity.Principal{{}, {ProjectScope: p.ProjectScope}, {ProjectScope: p.ProjectScope, SubjectKind: "workspace", SubjectID: "owner"}} {
		if _, err := s.IssueExecutorCredential(ctx, invalid, uuid.NewString(), ""); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid principal", err)
		}
	}
	input := environmentInput("unknown-creator", "self_hosted", "/workspace")
	input.Creator = p.Subject()
	session, err := s.CreateSession(ctx, p.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.GetSessionEnvironment(ctx, p.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := s.AuthenticateEnvironmentExecutor(ctx, target.ID, executorDigest(key.Token)); err != nil || owner != p.TenantID {
		t.Fatal("user principal failed", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE sessions SET creator_kind=NULL,creator_id=NULL WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, target.ID, executorDigest(key.Token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown creator accepted", err)
	}
	if _, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown creator claimed", err)
	}
}
