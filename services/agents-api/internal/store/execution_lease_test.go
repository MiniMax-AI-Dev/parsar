package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func executionLease(t *testing.T, s *Store) *ExecutionLease {
	t.Helper()
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	return lease
}

func TestExecutionLeaseLossFencesAllLifecycleWrites(t *testing.T) {
	s, pool := testStore(t)
	old := executionLease(t, s)
	writer := old.Store()
	tenant, active := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, active.ID, "active")
	transition(t, writer, tenant, active.ID, input.TurnID, TurnQueued, TurnInProgress)
	host, err := s.CreateDevice(t.Context(), tenant, "owner test", device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.BindSessionDevice(t.Context(), tenant, active.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	queued, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	pending := submitMessage(t, s, tenant, queued.ID, "queued")
	waiting, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "waiting"})
	if err != nil {
		t.Fatal(err)
	}
	waitInput := submitMessage(t, s, tenant, waiting.ID, "waiting")
	transition(t, writer, tenant, waiting.ID, waitInput.TurnID, TurnQueued, TurnInProgress)
	call := functionCallFixture("saved")
	if err = writer.RecordFunctionCall(t.Context(), tenant, waiting.ID, waitInput.TurnID, call); err != nil {
		t.Fatal(err)
	}
	if err = s.SubmitFunctionResult(t.Context(), tenant, waiting.ID, waitInput.TurnID, call.CallID, json.RawMessage(`{"success":true,"output":"saved"}`)); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetSession(t.Context(), tenant, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Kill only this test's owner connection. Do not notify the old writer by Ping.
	var killed bool
	if err = pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", old.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	successor := executionLease(t, s)
	mustReject := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("stale %s committed while successor owned lease", name)
		}
	}
	mustReject("binding", writer.BindSessionDevice(t.Context(), tenant, queued.ID, host.ID))
	_, err = writer.TransitionTurn(t.Context(), tenant, queued.ID, pending.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
	mustReject("claim", err)
	mustReject("journal", writer.AppendTurnEvents(t.Context(), tenant, active.ID, input.TurnID, 1, []ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"stale"}`)}}))
	mustReject("callback", writer.RecordFunctionCall(t.Context(), tenant, active.ID, input.TurnID, functionCallFixture("late")))
	mustReject("receipt", writer.ConfirmFunctionResult(t.Context(), tenant, waiting.ID, waitInput.TurnID, call.CallID))
	_, err = writer.CompleteExecution(t.Context(), tenant, active.ID, input.TurnID, TurnCompleted, json.RawMessage(`{"done":{"content":"stale"}}`), "stale-native", input.Sequence)
	mustReject("completion", err)
	_, err = writer.TransitionTurn(t.Context(), tenant, active.ID, input.TurnID, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed})
	mustReject("reconciliation", err)
	after, err := s.GetSession(t.Context(), tenant, active.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("stale state persisted", after, err)
	}
	afterCursor, err := s.SessionEventCursor(t.Context(), tenant, active.ID)
	if err != nil || afterCursor != cursor {
		t.Fatal("stale events published", afterCursor, err)
	}
	events, err := s.ListTurnEvents(t.Context(), tenant, active.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("stale journal persisted", events, err)
	}
	saved, err := s.GetFunctionCall(t.Context(), tenant, waiting.ID, waitInput.TurnID, call.CallID)
	if err != nil || saved.Applied {
		t.Fatal("stale receipt persisted", saved, err)
	}
	if _, err = s.GetSessionDevice(t.Context(), tenant, queued.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale binding persisted", err)
	}
	queuedTurn, err := s.GetTurn(t.Context(), tenant, queued.ID, pending.TurnID)
	if err != nil || queuedTurn.Status != TurnQueued {
		t.Fatal("queued work changed", queuedTurn, err)
	}
	// Public admission remains usable with a dead owner connection.
	submitMessage(t, s, tenant, queued.ID, "additional")
	if err = successor.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = successor.Store().CompleteExecution(t.Context(), tenant, active.ID, input.TurnID, TurnCompleted, json.RawMessage(`{"done":{"content":"accepted"}}`), "successor-native", input.Sequence); err != nil {
		t.Fatal(err)
	}
	bound, err := s.GetSessionDevice(t.Context(), tenant, active.ID)
	if err != nil || bound.NativeSessionID != "successor-native" {
		t.Fatal(bound, err)
	}
	_, err = successor.Store().TransitionTurn(t.Context(), tenant, active.ID, input.TurnID, TurnTransition{ExpectedStatus: TurnInProgress, Status: TurnFailed})
	if !errors.Is(err, ErrTurnConflict) {
		t.Fatal("terminal CAS changed", err)
	}
	if err = successor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustReject("closed writer", successor.Store().BindSessionDevice(t.Context(), tenant, queued.ID, host.ID))
}

func TestExecutionLeaseSerializesWritesAndPings(t *testing.T) {
	s, _ := testStore(t)
	lease := executionLease(t, s)
	type work struct{ tenant, session, turn string }
	tasks := make([]work, 4)
	for i := range tasks {
		tenant, session := newTurnSession(t, s)
		tasks[i] = work{tenant, session.ID, submitMessage(t, s, tenant, session.ID, "start").TurnID}
	}
	var group sync.WaitGroup
	results := make(chan error, len(tasks)*2)
	for _, task := range tasks {
		group.Go(func() {
			_, err := lease.Store().TransitionTurn(t.Context(), task.tenant, task.session, task.turn, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
			if err == nil {
				err = lease.Store().AppendTurnEvents(t.Context(), task.tenant, task.session, task.turn, 1, []ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"accepted"}`)}})
			}
			results <- err
		})
		group.Go(func() { results <- lease.Ping(t.Context()) })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	// While the owner connection is in use, public admission on another Session
	// does not need that connection. A waiting owner operation can be cancelled.
	if err := lease.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer lease.unlock()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	task := tasks[0]
	if _, err := s.SubmitMessage(ctx, task.tenant, task.session, "public", json.RawMessage(`{"text":"additional"}`)); err != nil {
		t.Fatal("public admission used owner gate", err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := lease.Ping(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("gate wait ignored cancellation", err)
	}
}

func TestExecutionLeaseBoundsSessionLockWait(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	tenant, session := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, session.ID, "start")
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(t.Context(), "SELECT id FROM sessions WHERE id=$1 FOR UPDATE", session.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	start := time.Now()
	_, err = lease.Store().TransitionTurn(ctx, tenant, session.ID, input.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) >= 7*time.Second {
		t.Fatal("owner transaction did not enforce its shorter deadline", err)
	}
	if err = blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetTurn(t.Context(), tenant, session.ID, input.TurnID)
	if err != nil || turn.Status != TurnQueued {
		t.Fatal("timed-out claim changed queued work", turn, err)
	}
}
