package store

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func localEnvironment(t *testing.T, s *Store, tenant string) (Session, Environment) {
	t.Helper()
	session, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{
		Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session, environment
}

func TestEnvironmentDeviceAuthorityAndLifecycle(t *testing.T) {
	s, _ := testStore(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	sibling, _ := localEnvironment(t, s, tenant)
	foreign, _ := localEnvironment(t, s, foreignTenant)
	digest := device.HashCredential(uuid.NewString())
	if _, err := s.CreateEnvironmentDevice(t.Context(), foreignTenant, environment.ID, "foreign", digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign provisioning: %v", err)
	}
	bound, err := s.CreateEnvironmentDevice(t.Context(), tenant, environment.ID, "dedicated", digest)
	if err != nil || bound.EnvironmentID != environment.ID {
		t.Fatalf("provision: %+v %v", bound, err)
	}
	for _, other := range []Session{sibling, foreign} {
		if err := s.BindSessionDevice(t.Context(), other.TenantID, other.ID, bound.ID); err == nil {
			t.Fatal("dedicated credential bound to another Session")
		}
	}
	devices, err := s.ListExecutionDevices(t.Context(), tenant)
	if err != nil || len(devices) != 0 {
		t.Fatalf("dedicated device entered general selection: %v %v", devices, err)
	}
	reopened, _ := testStore(t)
	got, err := reopened.GetSessionDevice(t.Context(), tenant, session.ID)
	if err != nil || got != bound {
		t.Fatalf("durable exact binding: %+v %v", got, err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), bound.ID); err != nil || !ok {
		t.Fatalf("valid credential unavailable: %v", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), bound.ID); err != nil || ok {
		t.Fatalf("deleted Environment still authenticates: %v", err)
	}
	status, err := s.TouchRuntimeHeartbeat(t.Context(), bound.ID)
	if err != nil || !status.Deleted {
		t.Fatalf("deleted Environment heartbeat: %+v %v", status, err)
	}
}

func TestEnvironmentDeviceProvisioningHasOneWinner(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateEnvironmentDevice(t.Context(), tenant, environment.ID, "runtime", device.HashCredential(uuid.NewString()))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrDeviceBindingConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("provisioned %d devices", winners)
	}
	bound, err := s.GetSessionDevice(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(t.Context(), tenant, bound.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEnvironmentDevice(t.Context(), tenant, environment.ID, "replacement", device.HashCredential(uuid.NewString())); !errors.Is(err, ErrDeviceBindingConflict) {
		t.Fatalf("silent placement replacement: %v", err)
	}
}
