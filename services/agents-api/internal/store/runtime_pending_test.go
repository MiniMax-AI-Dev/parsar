package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestManagedRuntimeAutomaticBootstrapRecoversCommittedSessions(t *testing.T) {
	s, _ := store.NewTestStore(t)
	tenant, idle, idleEnvironment := managedSession(t, s)
	initial, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted"}}`), InitialInputs: []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"hello"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, deleted, deletedEnvironment := managedSession(t, s)
	if err := s.DeleteSession(t.Context(), deleted.TenantID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	start := func() *execution.Worker {
		w, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry(), ManagedRuntimes: &execution.RuntimeProviders{CoreURL: "http://core.invalid/api/v1", DefaultProvider: key, Providers: map[string]sandbox.Provider{key: p}}})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	stop := func(w *execution.Worker) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = w.Run(ctx)
	}
	w := start()
	closed := false
	t.Cleanup(func() {
		if !closed {
			stop(w)
		}
	})
	// Both Sessions committed before this Worker existed, including one without inputs.
	for range 100 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	idleOwner, err := s.GetRuntimeAllocation(t.Context(), tenant, idleEnvironment.ID)
	if err != nil || idleOwner.State != "running" {
		t.Fatal("idle creation was stranded", idleOwner, err)
	}
	initialOwner, err := s.GetRuntimeAllocation(t.Context(), tenant, initial.Environment.ID)
	if err != nil || initialOwner.State != "running" {
		t.Fatal("initial creation was stranded", initialOwner, err)
	}
	if _, err := s.GetRuntimeAllocation(t.Context(), deleted.TenantID, deletedEnvironment.ID); err == nil {
		t.Fatal("deleted Session provisioned")
	}
	waiting, err := s.GetSession(t.Context(), tenant, initial.ID)
	if err != nil || waiting.LastTurn != nil || waiting.EnvironmentInputActivity != nil {
		t.Fatal("compute existence claimed input readiness", waiting, err)
	}
	quiet, err := s.GetSession(t.Context(), tenant, idle.ID)
	if err != nil || quiet.LastTurn != nil || quiet.EnvironmentInputActivity != nil {
		t.Fatal("idle creation fabricated work", err)
	}
	creates := p.creates
	stop(w)
	closed = true
	next := start()
	defer stop(next)
	for range 100 {
		if err := next.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if p.creates != creates {
		t.Fatal("restart repeated bootstrap", creates, p.creates)
	}
	for _, owner := range []store.RuntimeAllocation{idleOwner, initialOwner} {
		got, err := s.GetRuntimeAllocation(t.Context(), tenant, owner.EnvironmentID)
		if err != nil || got.ID != owner.ID || got.DeviceID != owner.DeviceID {
			t.Fatal("restart replaced allocation identity", got, err)
		}
	}
}
