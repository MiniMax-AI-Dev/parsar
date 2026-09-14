package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentConnectionWorkerReconcilesAndClosesBeforeLease(t *testing.T) {
	s, _ := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "connection-worker", Configuration: []byte(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	generation := uuid.NewString()
	if err := lease.Store().ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err := lease.Store().ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	var worker *execution.Worker
	closed := false
	next := uuid.NewString()
	dispatcher := &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry(), CloseEnvironmentConnections: func() {
		closed = true
		if err := worker.CheckOwnership(context.Background()); err != nil {
			t.Error("shutdown lost ownership before connections", err)
		}
		if err := worker.ObserveEnvironmentConnection(context.Background(), tenant, environment.ID, next, 2, false); err != nil {
			t.Error(err)
		}
	}}
	worker, err = execution.StartWorker(t.Context(), dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() { defer close(exited); done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Error("worker cleanup did not exit")
		}
	})
	awaitEnvironmentConnectionState(t, ctx, s, tenant, environment.ID, "disconnected")
	if err := worker.ReplaceEnvironmentConnection(ctx, tenant, environment.ID, next); err != nil {
		t.Fatal(err)
	}
	if err := worker.ObserveEnvironmentConnection(ctx, tenant, environment.ID, next, 1, true); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not close")
	}
	if !closed {
		t.Fatal("worker skipped connection shutdown")
	}
	awaitEnvironmentConnectionState(t, t.Context(), s, tenant, environment.ID, "disconnected")
	if err := worker.CheckOwnership(t.Context()); err == nil {
		t.Fatal("worker retained lease")
	}
	if len(retainedEnvironmentEvents(t, t.Context(), s, tenant, session.ID, environment.ID)) != 4 {
		t.Fatal("worker lifecycle did not retain all snapshots")
	}
}
