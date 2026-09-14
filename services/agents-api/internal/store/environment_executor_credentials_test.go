package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func executorDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestEnvironmentExecutorCredentialLifecycle(t *testing.T) {
	s, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	ctx := t.Context()
	principal := FixtureExecutorPrincipal(t, s, tenant)
	session, err := s.CreateSession(ctx, tenant, environmentInput("credential", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueExecutorCredential(ctx, FixtureExecutorPrincipal(t, s, foreign), environment.ID, environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign issue", err)
	}
	if _, err := s.RotateExecutorCredential(ctx, principal, environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("rotate manufactured a credential", err)
	}
	issued, err := s.IssueExecutorCredential(ctx, principal, environment.ID, environment.ID)
	token := issued.Token
	if err != nil || len(token) != 43 {
		t.Fatal("issue failed", err)
	}
	check := func(st *Store, token string, allowed bool) {
		t.Helper()
		owner, err := st.AuthenticateEnvironmentExecutor(ctx, environment.ID, executorDigest(token))
		if allowed {
			if err != nil || owner != tenant {
				t.Fatal("credential not accepted for owner", err)
			}
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal("invalid credential accepted", err)
		}
	}
	check(s, token, true)
	check(s, "caller/device/harness/grant", false)
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, uuid.NewString(), executorDigest(token)); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign Environment accepted", err)
	}
	if _, err := s.IssueExecutorCredential(ctx, principal, environment.ID, environment.ID); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("issue silently replaced credential", err)
	}
	if err := s.RevokeExecutorCredential(ctx, FixtureExecutorPrincipal(t, s, foreign), environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign revoke", err)
	}
	if _, err := s.RotateExecutorCredential(ctx, FixtureExecutorPrincipal(t, s, foreign), environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign rotate", err)
	}
	var stored string
	if err := pool.QueryRow(ctx, "SELECT token_sha256 FROM environment_executor_credentials WHERE environment_id=$1", environment.ID).Scan(&stored); err != nil || stored != executorDigest(token) {
		t.Fatal("digest persistence", err)
	}
	// Executor authority does not inherit the five-minute connection grant lifetime.
	if _, err := pool.Exec(ctx, "UPDATE environment_executor_credentials SET issued_at=now()-interval '1 day' WHERE environment_id=$1", environment.ID); err != nil {
		t.Fatal(err)
	}
	restarted, _ := testStore(t)
	check(restarted, token, true)
	next, err := restarted.RotateExecutorCredential(ctx, principal, environment.ID)
	if err != nil || next.Token == token {
		t.Fatal("rotation failed", err)
	}
	check(s, token, false)
	check(s, next.Token, true)
	for range 2 {
		if err := s.RevokeExecutorCredential(ctx, principal, environment.ID); err != nil {
			t.Fatal(err)
		}
	}
	check(restarted, next.Token, false)
	if _, err := s.IssueExecutorCredential(ctx, principal, environment.ID, environment.ID); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("ordinary issue resurrected revoked authority", err)
	}
	restored, err := s.RotateExecutorCredential(ctx, principal, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(restarted, next.Token, false)
	check(restarted, restored.Token, true)
	if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	check(restarted, restored.Token, false)
	if _, err := s.RotateExecutorCredential(ctx, principal, environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Session authority resurrected", err)
	}
	if err := pool.QueryRow(ctx, "SELECT token_sha256 FROM environment_executor_credentials WHERE environment_id=$1", environment.ID).Scan(&stored); err != nil || stored != executorDigest(restored.Token) {
		t.Fatal("deleted ownership was destroyed", err)
	}
}

func TestEnvironmentExecutorConcurrentIssueAndDeletion(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	principal := FixtureExecutorPrincipal(t, s, tenant)
	session, err := s.CreateSession(ctx, tenant, environmentInput("concurrent-key", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Provisioning remains control-plane work while the execution owner is active.
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(ctx)
	const attempts = 8
	var wg sync.WaitGroup
	tokens := make(chan string, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			if i%2 != 0 {
				st = other
			}
			token, err := st.IssueExecutorCredential(ctx, principal, environment.ID, environment.ID)
			if err == nil {
				tokens <- token.Token
			} else if !errors.Is(err, ErrExecutorCredentialExists) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(tokens)
	if len(tokens) != 1 {
		t.Fatal("issue did not choose one winner", len(tokens))
	}
	original := <-tokens
	rotated := make(chan string, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		token, err := other.RotateExecutorCredential(ctx, principal, environment.ID)
		if err == nil {
			rotated <- token.Token
		} else if !errors.Is(err, ErrNotFound) {
			t.Error(err)
		}
	}()
	if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(rotated)
	for token := range rotated {
		if _, err := s.AuthenticateEnvironmentExecutor(ctx, environment.ID, executorDigest(token)); !errors.Is(err, ErrNotFound) {
			t.Fatal("racing rotation authorized deleted Environment", err)
		}
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, environment.ID, executorDigest(original)); !errors.Is(err, ErrNotFound) {
		t.Fatal("original key survived deletion", err)
	}
}
