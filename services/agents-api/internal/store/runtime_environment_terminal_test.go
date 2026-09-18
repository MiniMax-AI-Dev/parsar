package store

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestManagedEnvironmentTerminationSettlesInputAndPreservesIdentity(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "expired"}[expired], func(t *testing.T) {
			s, pool := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("initial-terminal", "openai_hosted", "/workspace")
			input.InitialInputs = []Input{messageInput("initial")}
			session, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			reservation := initialEnvironmentReservation(t, s, pool, tenant, session.ID)
			writer := executionLease(t, s).Store()
			owner, err := writer.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, uuid.NewString(), device.HashCredential(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				if _, err := pool.Exec(t.Context(), "UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '61 minutes' WHERE id=$1", owner.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := writer.RequestRuntimeCleanup(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			ended, err := s.GetSession(t.Context(), tenant, session.ID)
			status := "failed"
			if expired {
				status = "expired"
			}
			if err != nil || ended.Environment.Status != status || ended.LastTurn != nil || ended.EnvironmentInputActivity == nil || ended.EnvironmentInputActivity.Status != "failed" || ended.EnvironmentInputActivity.Failure != "environment_unavailable" {
				t.Fatal("terminal projection", ended, err)
			}
			if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
				t.Fatal("terminal credential remained usable", err)
			}
			failed, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID)
			if err != nil || failed.State != EnvironmentInputFailed || len(failed.Receipts) != 0 || failed.SettledAt == nil || !failed.Deadline.Equal(reservation.Deadline) {
				t.Fatal("late preparation resurrected failed input", failed, err)
			}
			if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "new", []Input{messageInput("later")}); !errors.Is(err, ErrEnvironmentUnavailable) {
				t.Fatal("terminal environment admitted new input", err)
			}
			if _, err := s.CreateSession(t.Context(), tenant, input); err != nil {
				t.Fatal("matching creation retry changed outcome", err)
			}
			events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			expected := 2
			if expired {
				expected = 1
			}
			if len(events) != expected || events[len(events)-1].Event.Type != "agent.session.failed" {
				t.Fatal("wrong terminal events", events)
			}
			if !expired && (events[0].Event.Type != "agent.session.environment.failed" || events[0].Event.Environment.Error == nil) {
				t.Fatal("missing safe environment failure", events)
			}
			cursor, _ := s.SessionEventCursor(t.Context(), tenant, session.ID)
			if _, err := writer.RequestRuntimeCleanup(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			if next, err := s.SessionEventCursor(t.Context(), tenant, session.ID); err != nil || next != cursor {
				t.Fatal("cleanup repeated terminal events", next, err)
			}
			if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, session.Environment.ID, uuid.NewString()); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("late connection revived terminal environment", err)
			}
			if _, err := writer.ReleaseRuntimeAllocation(t.Context(), owner); !errors.Is(err, ErrTurnConflict) {
				t.Fatal("unknown creation was forgotten", err)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
		})
	}
}

func TestManagedEnvironmentFailureRollsBackWithSessionEvent(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("rollback-terminal", "openai_hosted", "/workspace")
	input.InitialInputs = []Input{messageInput("initial")}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	writer := executionLease(t, s).Store()
	owner, err := writer.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, uuid.NewString(), device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	constraint := pgx.Identifier{"terminal_failure_" + uuid.NewString()[:8]}.Sanitize()
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (session_id <> '"+session.ID+"' OR payload->'event'->>'type' <> 'agent.session.failed') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE session_events DROP CONSTRAINT IF EXISTS "+constraint)
	})
	if _, err := writer.RequestRuntimeCleanup(t.Context(), owner); err == nil {
		t.Fatal("cleanup committed without failure event")
	}
	current, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || current.Environment.Status != "pending" || current.EnvironmentInputActivity != nil {
		t.Fatal("partial public termination", err)
	}
	if reservation := initialEnvironmentReservation(t, s, pool, tenant, session.ID); reservation.State != EnvironmentInputPending {
		t.Fatal("partial input failure", reservation)
	}
	allocation, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || allocation.State != "creating" {
		t.Fatal("partial allocation transition", err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || !ok {
		t.Fatal("partial credential revocation", err)
	}
}
