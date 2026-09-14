package codex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func issueHarness(t *testing.T, f fixture, owner context.Context) (string, func()) {
	t.Helper()
	key := f.keys[0]
	token, release, err := f.registry.IssueHarnessCredential(owner, key.TenantID, key.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return token, release
}

func TestHarnessCredentialCapacityAndOwnership(t *testing.T) {
	f := newFixture(t)
	k := f.keys[0]
	for _, ids := range [][2]string{{"invalid", k.EnvironmentID}, {k.TenantID, "invalid"}, {uuid.Nil.String(), k.EnvironmentID}} {
		if _, _, err := f.registry.IssueHarnessCredential(t.Context(), ids[0], ids[1]); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatal("invalid ownership accepted", err)
		}
	}
	for _, ids := range [][2]string{{f.keys[1].TenantID, k.EnvironmentID}, {k.TenantID, uuid.NewString()}} {
		if _, _, err := f.registry.IssueHarnessCredential(t.Context(), ids[0], ids[1]); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("foreign or absent Environment accepted", err)
		}
	}
	owner, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := f.registry.IssueHarnessCredential(owner, k.TenantID, k.EnvironmentID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled owner accepted", err)
	}
	var firstToken string
	var firstRelease func()
	for i := range maxHarnessCredentials {
		token, release := issueHarness(t, f, t.Context())
		if i == 0 {
			firstToken, firstRelease = token, release
		}
	}
	if _, _, err := f.registry.IssueHarnessCredential(t.Context(), k.TenantID, k.EnvironmentID); !errors.Is(err, ErrHarnessCapacity) {
		t.Fatal("credential capacity not enforced", err)
	}
	firstRelease()
	firstRelease()
	next, _ := issueHarness(t, f, t.Context())
	if next == firstToken {
		t.Fatal("replacement credential reused bearer")
	}
	nativePOST(t, f, k.EnvironmentID, "connect", firstToken, ConnectRequest{nativeRequest().ExecutorPublicKey}, 401, nil)
	f.registry.Close()
	if _, _, err := f.registry.IssueHarnessCredential(t.Context(), k.TenantID, k.EnvironmentID); err == nil {
		t.Fatal("closed registry issued a credential")
	}
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	if len(f.registry.harnessKeys) != 0 {
		t.Fatal("closed registry retained credentials")
	}
}

func TestHarnessCredentialReleaseFencesGrantsAndPreservesSuccessor(t *testing.T) {
	for _, cause := range []string{"release", "owner", "shutdown"} {
		t.Run(cause, func(t *testing.T) {
			f := newFixture(t)
			owner, cancel := context.WithCancel(t.Context())
			defer cancel()
			token, release := issueHarness(t, f, owner)
			k := f.keys[0]
			reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
			executor := dial(t, reg.URL)
			defer executor.Close()
			grant := harnessMaterial(t, f, token)
			harness := dial(t, grant.URL)
			defer harness.Close()
			validateGrant(t, f, validationBody(grant), true)
			pending := harnessMaterial(t, f, token)
			switch cause {
			case "release":
				release()
			case "owner":
				cancel()
			case "shutdown":
				f.registry.Close()
			}
			nativePOST(t, f, k.EnvironmentID, "connect", token, ConnectRequest{nativeRequest().ExecutorPublicKey}, 401, nil)
			expectClosed(t, harness)
			expectClosed(t, executor)
			awaitLifecycle(t, f, k.EnvironmentID, reg.ExecutorRegistrationID, 2, false)
			rejectSocket(t, pending.URL, 401)
			if cause == "shutdown" {
				return
			}
			nextToken, _ := issueHarness(t, f, t.Context())
			nextExecutor := dial(t, reg.URL)
			defer nextExecutor.Close()
			nextGrant := harnessMaterial(t, f, nextToken)
			nextHarness := dial(t, nextGrant.URL)
			defer nextHarness.Close()
			release()
			validateGrant(t, f, validationBody(grant), false)
			validateGrant(t, f, validationBody(nextGrant), true)
			relayBytes(t, nextHarness, nextExecutor, []byte("successor survives old release"))
		})
	}
}

type delayedHarnessOwnership struct {
	EnvironmentStore
	armed    atomic.Bool
	observed chan struct{}
	resume   chan struct{}
}

func (s *delayedHarnessOwnership) GetEnvironment(ctx context.Context, tenant, environment string) (store.Environment, error) {
	value, err := s.EnvironmentStore.GetEnvironment(ctx, tenant, environment)
	if s.armed.Swap(false) {
		close(s.observed)
		select {
		case <-s.resume:
		case <-ctx.Done():
		}
	}
	return value, err
}

func TestHarnessReleaseFencesAuthorizationAlreadyInFlight(t *testing.T) {
	for _, operation := range []string{"connect", "attach", "validate"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			source := &delayedHarnessOwnership{EnvironmentStore: f.source, observed: make(chan struct{}), resume: make(chan struct{})}
			f.registry.source = source
			token, release := issueHarness(t, f, t.Context())
			reg := f.register(t, f.keys[0].EnvironmentID, f.tokens[0], nativeRequest(), 200)
			executor := dial(t, reg.URL)
			defer executor.Close()
			var grant ConnectResponse
			if operation != "connect" {
				grant = harnessMaterial(t, f, token)
			}
			if operation == "validate" {
				harness := dial(t, grant.URL)
				defer harness.Close()
			}
			source.armed.Store(true)
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch operation {
				case "connect":
					nativePOST(t, f, f.keys[0].EnvironmentID, "connect", token, ConnectRequest{nativeRequest().ExecutorPublicKey}, 401, nil)
				case "attach":
					rejectSocket(t, grant.URL, 401)
				case "validate":
					validateGrant(t, f, validationBody(grant), false)
				}
			}()
			<-source.observed
			release()
			close(source.resume)
			<-done
			f.registry.mu.Lock()
			remaining := len(f.registry.registrations[f.keys[0].EnvironmentID].grants)
			f.registry.mu.Unlock()
			if remaining != 0 {
				t.Fatal("in-flight authorization recreated a released grant")
			}
		})
	}
}
