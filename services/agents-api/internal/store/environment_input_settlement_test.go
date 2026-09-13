package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvironmentInputTerminalReservationsCannotRestart(t *testing.T) {
	for _, terminal := range []string{EnvironmentInputCancelled, EnvironmentInputExpired} {
		t.Run(terminal, func(t *testing.T) {
			s, pool := testStore(t)
			lease := executionLease(t, s)
			writer := lease.Store()
			tenant, session := environmentInputSession(t, s)
			ctx := context.Background()
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			early, err := s.ExpireEnvironmentInput(ctx, tenant, session.ID, pending.ID)
			if err != nil || early.State != EnvironmentInputPending || early.SettledAt != nil || !early.Deadline.Equal(pending.Deadline) {
				t.Fatal("early expiry", early, err)
			}
			var settled EnvironmentInputReservation
			if terminal == EnvironmentInputExpired {
				if _, err := pool.Exec(ctx, "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
					t.Fatal(err)
				}
				settled, err = writer.PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID)
			} else {
				settled, err = s.CancelEnvironmentInput(ctx, tenant, session.ID, pending.ID)
			}
			if err != nil || settled.State != terminal || settled.SettledAt == nil || len(settled.Receipts) != 0 {
				t.Fatal("terminal settlement", settled, err)
			}
			retry, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "pending", pending.Inputs)
			if err != nil || retry.State != terminal || retry.ID != pending.ID || !retry.Deadline.Equal(settled.Deadline) || !retry.SettledAt.Equal(*settled.SettledAt) {
				t.Fatal("terminal retry changed outcome", retry, err)
			}
			if _, err := s.SubmitInputs(ctx, tenant, session.ID, "pending", pending.Inputs); !errors.Is(err, ErrTurnConflict) {
				t.Fatal("terminal request reopened through direct path", err)
			}
			if _, err := s.SubmitInputs(ctx, tenant, session.ID, "pending", []Input{messageInput("changed")}); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("terminal identity changed", err)
			}
			later := reserveEnvironmentInput(t, s, tenant, session.ID, "later")
			for _, finish := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){
				writer.PromoteEnvironmentInput, s.CancelEnvironmentInput, s.ExpireEnvironmentInput,
			} {
				got, err := finish(ctx, tenant, session.ID, pending.ID)
				if err != nil || got.State != terminal {
					t.Fatal("old settlement changed", got, err)
				}
			}
			got, err := s.GetEnvironmentInputReservation(ctx, tenant, session.ID, later.ID)
			if err != nil || got.State != EnvironmentInputPending || !got.Deadline.Equal(later.Deadline) {
				t.Fatal("old settlement touched successor", got, err)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
		})
	}
}

func TestEnvironmentInputPromotionRollsBackHistoryAndSettlement(t *testing.T) {
	for _, phase := range []string{"input", "settlement", "claim", "claim-event"} {
		t.Run(phase, func(t *testing.T) {
			s, pool := testStore(t)
			lease := executionLease(t, s)
			writer := lease.Store()
			tenant, session := environmentInputSession(t, s)
			ctx := context.Background()
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			name := "reservation_failure_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			table := "turn_inputs"
			expression := "session_id <> '" + session.ID + "'::uuid OR payload->>'text' <> 'second'"
			if phase == "settlement" {
				table = "environment_input_reservations"
				expression = "id <> '" + pending.ID + "'::uuid OR state <> 'admitted'"
			}
			if phase == "claim" {
				table = "turns"
				expression = "session_id <> '" + session.ID + "'::uuid OR status <> 'in_progress'"
			}
			if phase == "claim-event" {
				table = "session_events"
				expression = "session_id <> '" + session.ID + "'::uuid OR payload->'event'->>'type' <> 'agent.session.turn.in_progress'"
			}
			if _, err := pool.Exec(ctx, "ALTER TABLE "+table+" ADD CONSTRAINT "+name+" CHECK ("+expression+") NOT VALID"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, "ALTER TABLE "+table+" DROP CONSTRAINT IF EXISTS "+name) })
			if _, err := writer.PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID); err == nil {
				t.Fatal("injected failure succeeded")
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
			got, err := s.GetEnvironmentInputReservation(ctx, tenant, session.ID, pending.ID)
			if err != nil || got.State != EnvironmentInputPending || got.SettledAt != nil || !got.Deadline.Equal(pending.Deadline) {
				t.Fatal("partial settlement survived", got, err)
			}
			if _, err := pool.Exec(ctx, "ALTER TABLE "+table+" DROP CONSTRAINT "+name); err != nil {
				t.Fatal(err)
			}
			got, err = writer.PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID)
			if err != nil || got.State != EnvironmentInputAdmitted {
				t.Fatal(got, err)
			}
			environmentInputHistory(t, pool, session.ID, 1, 2)
		})
	}
}

func TestEnvironmentInputDeadlineIsCheckedAfterSessionLock(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	writer := lease.Store()
	tenant, session := environmentInputSession(t, s)
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blocker int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM sessions WHERE id=$1 FOR UPDATE", session.ID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		value EnvironmentInputReservation
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := writer.PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID)
		done <- outcome{got, err}
	}()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case result := <-done:
			t.Fatal("promotion bypassed Session lock", result)
		case <-ctx.Done():
			t.Fatal("promotion lock wait not observed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Transaction-start time is now older than the controlled deadline.
	if _, err := tx.Exec(ctx, "UPDATE environment_input_reservations SET deadline=clock_timestamp() WHERE id=$1", pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.value.State != EnvironmentInputExpired || result.value.SettledAt == nil {
		t.Fatal("lock wait extended input lifetime", result)
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	stored, err := s.GetEnvironmentInputReservation(ctx, tenant, session.ID, pending.ID)
	if err != nil || stored.State != EnvironmentInputExpired {
		t.Fatal("expiry was rolled back", stored, err)
	}
}

func TestEnvironmentInputCancelAndPromotionShareOneOutcome(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	writer := lease.Store()
	other, _ := testStore(t)
	tenant, session := environmentInputSession(t, s)
	ctx := context.Background()
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	start := make(chan struct{})
	results := make(chan EnvironmentInputReservation, 2)
	errs := make(chan error, 2)
	for _, finish := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){
		writer.PromoteEnvironmentInput, other.CancelEnvironmentInput,
	} {
		go func() {
			<-start
			got, err := finish(ctx, tenant, session.ID, pending.ID)
			results <- got
			errs <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if first.State != second.State || (first.State != EnvironmentInputAdmitted && first.State != EnvironmentInputCancelled) {
		t.Fatal("competing settlements diverged", first.State, second.State)
	}
	turns, inputs := 0, 0
	if first.State == EnvironmentInputAdmitted {
		turns, inputs = 1, 2
	}
	environmentInputHistory(t, pool, session.ID, turns, inputs)
}

func TestEnvironmentInputDeletionSettlesPendingAndFencesPromotion(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "racing-promotion"}[concurrent], func(t *testing.T) {
			s, pool := testStore(t)
			lease := executionLease(t, s)
			writer := lease.Store()
			tenant, session := environmentInputSession(t, s)
			ctx := context.Background()
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			done := make(chan error, 1)
			if concurrent {
				go func() {
					_, err := writer.PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID)
					done <- err
				}()
			}
			if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			if concurrent {
				if err := <-done; err != nil && !errors.Is(err, ErrNotFound) {
					t.Fatal(err)
				}
			}
			for _, action := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){
				s.GetEnvironmentInputReservation, writer.PromoteEnvironmentInput, s.CancelEnvironmentInput,
			} {
				if _, err := action(ctx, tenant, session.ID, pending.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("deleted reservation remained accessible", err)
				}
			}
			if _, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "late", pending.Inputs); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted Session accepted reservation", err)
			}
			var state string
			var active int
			if err := pool.QueryRow(ctx, "SELECT state FROM environment_input_reservations WHERE id=$1", pending.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != EnvironmentInputCancelled && (!concurrent || state != EnvironmentInputAdmitted) {
				t.Fatal("deletion lost pending settlement", state)
			}
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM turns WHERE session_id=$1 AND (status='queued' OR (status IN ('in_progress','waiting') AND cancel_requested_at IS NULL))", session.ID).Scan(&active); err != nil || active != 0 {
				t.Fatal("deleted reservation retained unclaimed or uncancelled work", active, err)
			}
		})
	}
}
