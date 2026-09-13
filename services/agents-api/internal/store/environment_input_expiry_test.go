package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestEnvironmentExpiryBoundsBatchAndRequiresExecutionWriter(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	var reservations []EnvironmentInputReservation
	for range 33 {
		tenant, session := environmentInputSession(t, s)
		pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
		reservations = append(reservations, pending)
		if _, err := pool.Exec(ctx, "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.ExpireEnvironmentInputs(ctx); err == nil || n != 0 {
		t.Fatal("unleased maintenance", n, err)
	}
	var before, after, due int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE state='expired'), count(*) FILTER (WHERE state='pending' AND deadline <= statement_timestamp()) FROM environment_input_reservations").Scan(&before, &due); err != nil {
		t.Fatal(err)
	}
	lease := executionLease(t, s)
	n, err := lease.Store().ExpireEnvironmentInputs(ctx)
	if err != nil || n != 32 {
		t.Fatal("unbounded or incomplete batch", n, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM environment_input_reservations WHERE state='expired'").Scan(&after); err != nil || after-before != n {
		t.Fatal("reported expiry did not match persisted settlement", before, after, n, err)
	}
	// Existing test rows may precede this fixture; each pass must make bounded progress.
	for remaining := due - n; remaining > 0; {
		n, err = lease.Store().ExpireEnvironmentInputs(ctx)
		if err != nil || n <= 0 || n > 32 {
			t.Fatal("expiry backlog did not progress", n, err)
		}
		remaining -= n
	}
	for _, pending := range reservations {
		var state string
		if err := pool.QueryRow(ctx, "SELECT state FROM environment_input_reservations WHERE id=$1", pending.ID).Scan(&state); err != nil || state != EnvironmentInputExpired {
			t.Fatal(state, err)
		}
		environmentInputHistory(t, pool, pending.SessionID, 0, 0)
	}
}

func TestEnvironmentExpiryFencesLostExecutionOwner(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := environmentInputSession(t, s)
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
		t.Fatal(err)
	}
	old := executionLease(t, s)
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1,1000)", old.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	successor := executionLease(t, s)
	if n, err := old.Store().ExpireEnvironmentInputs(t.Context()); err == nil || n != 0 {
		t.Fatal("lost owner expired input", n, err)
	}
	got, err := s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, pending.ID)
	if err != nil || got.State != EnvironmentInputPending {
		t.Fatal("lost owner wrote through the pool", got, err)
	}
	for got.State == EnvironmentInputPending {
		n, err := successor.Store().ExpireEnvironmentInputs(t.Context())
		if err != nil || n == 0 {
			t.Fatal("successor could not expire input", n, err)
		}
		got, err = s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.State != EnvironmentInputExpired {
		t.Fatal(got)
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
}

func TestEnvironmentExpirySerializesWithTargetedSettlement(t *testing.T) {
	for _, action := range []string{"promote", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session := environmentInputSession(t, s)
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
				t.Fatal(err)
			}
			lease := executionLease(t, s)
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() { <-start; _, err := lease.Store().ExpireEnvironmentInputs(t.Context()); results <- err }()
			go func() {
				<-start
				var err error
				switch action {
				case "promote":
					_, err = lease.Store().PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
				case "cancel":
					_, err = s.CancelEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
				case "delete":
					err = s.DeleteSession(t.Context(), tenant, session.ID)
				}
				results <- err
			}()
			close(start)
			for range 2 {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			var state string
			if err := pool.QueryRow(t.Context(), "SELECT state FROM environment_input_reservations WHERE id=$1", pending.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != EnvironmentInputExpired && (action != "delete" || state != EnvironmentInputCancelled) {
				t.Fatal("invalid competing settlement", state)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
			if action == "delete" {
				if _, err := lease.Store().PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("deleted input resurrected", err)
				}
				return
			}
			later := reserveEnvironmentInput(t, s, tenant, session.ID, uuid.NewString())
			for _, settle := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){lease.Store().PromoteEnvironmentInput, s.CancelEnvironmentInput, s.ExpireEnvironmentInput} {
				old, err := settle(t.Context(), tenant, session.ID, pending.ID)
				if err != nil || old.State != state {
					t.Fatal("old reservation changed", old, err)
				}
			}
			if _, err := lease.Store().ExpireEnvironmentInputs(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, later.ID)
			if err != nil || got.State != EnvironmentInputPending || !got.Deadline.Equal(later.Deadline) {
				t.Fatal("old settlement affected successor", got, err)
			}
		})
	}
}
