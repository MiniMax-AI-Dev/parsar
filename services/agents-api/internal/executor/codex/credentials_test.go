package codex

import (
	"context"
	"sync"
	"testing"
)

type delayedCredential struct {
	EnvironmentStore
	hash     string
	once     sync.Once
	observed chan struct{}
	release  chan struct{}
}

func (s *delayedCredential) AuthenticateEnvironmentExecutor(ctx context.Context, environment, hash string) (string, error) {
	tenant, err := s.EnvironmentStore.AuthenticateEnvironmentExecutor(ctx, environment, hash)
	if hash == s.hash {
		s.once.Do(func() {
			close(s.observed)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return tenant, err
}

func TestRotatedCredentialFencesDelayedRegistration(t *testing.T) {
	f := newFixture(t)
	key := f.keys[0]
	source := &delayedCredential{EnvironmentStore: f.source, hash: key.TokenSHA256, observed: make(chan struct{}), release: make(chan struct{})}
	f.registry.source = source
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 401)
	}()
	<-source.observed
	next := "new-executor-credential"
	f.source.mu.Lock()
	f.source.keys[key.EnvironmentID] = digest(next)
	f.source.mu.Unlock()
	reg := f.register(t, key.EnvironmentID, next, nativeRequest(), 200)
	socket := dial(t, reg.URL)
	defer socket.Close()
	close(source.release)
	<-done
	awaitPresence(t, f, key.EnvironmentID, true)
	f.registry.mu.Lock()
	current := f.registry.registrations[key.EnvironmentID].id
	f.registry.mu.Unlock()
	if current != reg.ExecutorRegistrationID {
		t.Fatal("delayed revoked request replaced the current registration")
	}
	nativePOST(t, f, key.EnvironmentID, "validate", f.tokens[0], ValidationRequest{}, 401, nil)
}
