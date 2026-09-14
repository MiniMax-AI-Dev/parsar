package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newEnvironmentExpiryReservation(t *testing.T, s *store.Store) (string, store.EnvironmentInputReservation) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(),
		Engine: "codex", IdempotencyKey: "environment",
		Configuration: json.RawMessage(`{"agent":{"model":"fixture-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"wait for the environment"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return tenant, pending
}

func makeEnvironmentExpiryDue(t *testing.T, pool *pgxpool.Pool, pending *store.EnvironmentInputReservation) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1 RETURNING deadline", pending.ID).Scan(&pending.Deadline); err != nil {
		t.Fatal(err)
	}
}

func startEnvironmentExpiryWorker(t *testing.T, d *execution.Dispatcher) (*execution.Worker, func()) {
	t.Helper()
	worker, err := execution.StartWorker(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Error("worker stopped with error", err)
				}
			case <-time.After(15 * time.Second):
				t.Error("worker did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return worker, stop
}

func waitEnvironmentExpiry(t *testing.T, s *store.Store, tenant string, pending store.EnvironmentInputReservation) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := s.GetEnvironmentInputReservation(t.Context(), tenant, pending.SessionID, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == store.EnvironmentInputExpired {
			if got.SettledAt == nil || !got.Deadline.Equal(pending.Deadline) || len(got.Receipts) != 0 {
				t.Fatal("expiry changed identity or created receipts", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not expire input", got.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertEnvironmentExpiryHasNoHistory(t *testing.T, pool *pgxpool.Pool, session string) {
	t.Helper()
	var count int
	err := pool.QueryRow(t.Context(), `
  SELECT (SELECT count(*) FROM turns WHERE session_id=$1)
       + (SELECT count(*) FROM turn_inputs WHERE session_id=$1)
       + (SELECT count(*) FROM session_items WHERE session_id=$1)
       + (SELECT count(*) FROM session_events WHERE session_id=$1
          AND (payload ? 'turn' OR payload->'event' ? 'item'))`, session).Scan(&count)
	if err != nil || count != 0 {
		t.Fatal("pre-Turn expiry created Turn or Item history", count, err)
	}
}

func TestWorkerEnvironmentExpiryWithoutDevicesAndAfterRestart(t *testing.T) {
	s, pool := store.NewTestStore(t)
	dueTenant, due := newEnvironmentExpiryReservation(t, s)
	futureTenant, future := newEnvironmentExpiryReservation(t, s)
	makeEnvironmentExpiryDue(t, pool, &due)
	d := &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry()}
	_, stop := startEnvironmentExpiryWorker(t, d)
	waitEnvironmentExpiry(t, s, dueTenant, due)
	got, err := s.GetEnvironmentInputReservation(t.Context(), futureTenant, future.SessionID, future.ID)
	if err != nil || got.State != store.EnvironmentInputPending || !got.Deadline.Equal(future.Deadline) {
		t.Fatal("future input changed", got, err)
	}
	stop()
	makeEnvironmentExpiryDue(t, pool, &future)
	_, stop = startEnvironmentExpiryWorker(t, d)
	waitEnvironmentExpiry(t, s, futureTenant, future)
	stop()
	assertEnvironmentExpiryHasNoHistory(t, pool, due.SessionID)
	assertEnvironmentExpiryHasNoHistory(t, pool, future.SessionID)
}
