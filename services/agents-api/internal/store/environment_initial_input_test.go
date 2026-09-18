package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func initialEnvironmentReservation(t *testing.T, s *Store, pool *pgxpool.Pool, tenant, session string) EnvironmentInputReservation {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), "SELECT id FROM environment_input_reservations WHERE session_id=$1 AND is_initial", session).Scan(&id); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.GetEnvironmentInputReservation(t.Context(), tenant, session, id)
	if err != nil || !reservation.IsInitial {
		t.Fatal("missing initial origin", reservation, err)
	}
	return reservation
}

func TestEnvironmentInitialExpiryRollsBackWithFailureEventAndSerializesPromotion(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("initial-failure-rollback", "self_hosted", "/workspace")
	input.InitialInputs = []Input{messageInput("initial")}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	reservation := initialEnvironmentReservation(t, s, pool, tenant, session.ID)
	if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", reservation.ID); err != nil {
		t.Fatal(err)
	}
	constraint := pgx.Identifier{"initial_failure_" + uuid.NewString()[:8]}.Sanitize()
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (session_id <> '"+session.ID+"' OR payload->'event'->>'type' <> 'agent.session.failed') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE session_events DROP CONSTRAINT IF EXISTS "+constraint)
	})
	writer := executionLease(t, s).Store()
	if _, err := writer.ExpireEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID); err == nil {
		t.Fatal("expiry committed without its failure event")
	}
	retained := initialEnvironmentReservation(t, s, pool, tenant, session.ID)
	if retained.State != EnvironmentInputPending || retained.SettledAt != nil {
		t.Fatal("failure event rollback lost reservation", retained)
	}
	requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", session.Environment.ID)
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	type result struct {
		reservation EnvironmentInputReservation
		err         error
	}
	results := make(chan result, 2)
	for _, settle := range []func(context.Context, string, string, string) (EnvironmentInputReservation, error){writer.PromoteEnvironmentInput, s.ExpireEnvironmentInput} {
		go func() { r, err := settle(t.Context(), tenant, session.ID, reservation.ID); results <- result{r, err} }()
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil || got.reservation.State != EnvironmentInputExpired || len(got.reservation.Receipts) != 0 {
			t.Fatal("expiry/promotion race started work", got)
		}
	}
	events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil || len(events) != 2 || events[1].Event.Type != "agent.session.failed" {
		t.Fatal("racing settlement duplicated or lost failure", events, err)
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
}

func TestEnvironmentInitialInputCreationRetainsCursorIdentityAndPromotion(t *testing.T) {
	for _, kind := range []string{"self_hosted", "openai_hosted"} {
		t.Run(kind, func(t *testing.T) {
			s, pool := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("initial", kind, "/workspace")
			input.InitialInputs = []Input{messageInput("first"), messageInput("second")}
			creation, err := s.CreateSessionStream(t.Context(), tenant, input)
			if err != nil || !creation.Created || creation.Cursor != 0 || creation.Session.LastTurn != nil || creation.Session.EnvironmentInputActivity != nil {
				t.Fatal("creation snapshot borrowed initial work", creation, err)
			}
			session := creation.Session
			reservation := initialEnvironmentReservation(t, s, pool, tenant, session.ID)
			storedBatch, marshalErr := json.Marshal(reservation.Inputs)
			originalBatch, _ := json.Marshal(input.InitialInputs)
			if marshalErr != nil || reservation.State != EnvironmentInputPending || reservation.Deadline.Sub(reservation.CreatedAt) != 5*time.Minute || string(storedBatch) != string(originalBatch) {
				t.Fatal("initial batch/deadline changed", reservation)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
			status, actionEnvironment := "requires_action", session.Environment.ID
			if kind == "openai_hosted" {
				status, actionEnvironment = "", ""
			}
			requireEnvironmentInputActivity(t, s, tenant, session.ID, status, actionEnvironment)
			events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, creation.Cursor)
			expectedEvents := 1
			if kind == "openai_hosted" {
				expectedEvents = 0
			}
			if err != nil || len(events) != expectedEvents || (expectedEvents > 0 && (events[0].Event.Type != "agent.session."+status || events[0].Turn != nil)) {
				t.Fatal("creation cursor lost initial activity", events, err)
			}
			other, _ := testStore(t)
			retry, err := other.CreateSessionStream(t.Context(), tenant, input)
			if err != nil || retry.Created || retry.Cursor != int64(expectedEvents) || retry.Session.ID != session.ID || retry.Session.EnvironmentInputActivity != nil {
				t.Fatal("retry changed creation cursor/snapshot", retry, err)
			}
			if got := initialEnvironmentReservation(t, other, pool, tenant, session.ID); !reflect.DeepEqual(got, reservation) {
				t.Fatal("retry changed initial reservation")
			}
			changed := input
			changed.InitialInputs = []Input{messageInput("different")}
			if _, err := other.CreateSession(t.Context(), tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("changed initial batch accepted", err)
			}
			changed = input
			changed.Creator.ID = "different-creator"
			if _, err := other.CreateSession(t.Context(), tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("changed creator accepted", err)
			}
			writer := executionLease(t, s).Store()
			generation := uuid.NewString()
			if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, session.Environment.ID, generation); err != nil {
				t.Fatal(err)
			}
			if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, session.Environment.ID, generation, 1, true); err != nil {
				t.Fatal(err)
			}
			connectedStatus := "idle"
			if kind == "openai_hosted" {
				connectedStatus = ""
			}
			requireEnvironmentInputActivity(t, s, tenant, session.ID, connectedStatus, "")
			environmentInputHistory(t, pool, session.ID, 0, 0)
			promoted, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID)
			if err != nil || promoted.State != EnvironmentInputAdmitted || !promoted.IsInitial || len(promoted.Receipts) != 2 {
				t.Fatal("initial batch did not promote", promoted, err)
			}
			active := requireEnvironmentInputActivity(t, s, tenant, session.ID, "", "")
			if active.LastTurn == nil || active.LastTurn.Status != TurnInProgress {
				t.Fatal("promotion did not claim its Turn", active.LastTurn)
			}
			replay, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID)
			if err != nil || len(replay.Receipts) != 2 || !replay.Receipts[0].Replayed || !replay.Receipts[1].Replayed {
				t.Fatal("promotion retry granted fresh receipts", replay, err)
			}
			if _, err := other.CreateSession(t.Context(), tenant, input); err != nil {
				t.Fatal(err)
			}
			environmentInputHistory(t, pool, session.ID, 1, 2)
			after, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil || !reflect.DeepEqual(events, after[:len(events)]) {
				t.Fatal("initial snapshot changed after promotion", err)
			}
		})
	}
}

func TestEnvironmentInitialInputExpiryHasNoTurnAndCannotReplay(t *testing.T) {
	for _, kind := range []string{"self_hosted", "openai_hosted"} {
		t.Run(kind, func(t *testing.T) {
			s, pool := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("initial-expiry", kind, "/workspace")
			input.InitialInputs = []Input{messageInput("private initial text")}
			session, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			reservation := initialEnvironmentReservation(t, s, pool, tenant, session.ID)
			if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", reservation.ID); err != nil {
				t.Fatal(err)
			}
			lease := executionLease(t, s)
			writer := lease.Store()
			for reservation.State == EnvironmentInputPending {
				count, err := writer.ExpireEnvironmentInputs(t.Context())
				if err != nil || count < 1 || count > 32 {
					t.Fatal("expiry made no bounded progress", count, err)
				}
				reservation = initialEnvironmentReservation(t, s, pool, tenant, session.ID)
			}
			failed := requireEnvironmentInputActivity(t, s, tenant, session.ID, "failed", "")
			if failed.Environment.Status != "pending" || failed.LastTurn != nil || !failed.EnvironmentInputActivity.LastActiveAt.Equal(*reservation.SettledAt) {
				t.Fatal("input expiry changed Environment/Turn", failed)
			}
			events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			expectedEvents := 2
			if kind == "openai_hosted" {
				expectedEvents = 1
			}
			if err != nil || len(events) != expectedEvents || events[len(events)-1].Event.Type != "agent.session.failed" || events[len(events)-1].Turn != nil || events[len(events)-1].EnvironmentInputActivity.Status != "failed" {
				t.Fatal("missing pre-Turn failure snapshot", events, err)
			}
			if err := lease.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			pool.Close()
			reopened, reopenedPool := testStore(t)
			writer = executionLease(t, reopened).Store()
			if _, err := reopened.CreateSession(t.Context(), tenant, input); err != nil {
				t.Fatal(err)
			}
			if got := initialEnvironmentReservation(t, reopened, reopenedPool, tenant, session.ID); !reflect.DeepEqual(got, reservation) {
				t.Fatal("reopened creation retry reset expiry", got)
			}
			generation := uuid.NewString()
			if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, session.Environment.ID, generation); err != nil {
				t.Fatal(err)
			}
			if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, session.Environment.ID, generation, 1, true); err != nil {
				t.Fatal(err)
			}
			late, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID)
			if err != nil || late.State != EnvironmentInputExpired || len(late.Receipts) != 0 {
				t.Fatal("late connection resurrected initial input", late, err)
			}
			requireEnvironmentInputActivity(t, reopened, tenant, session.ID, "failed", "")
			environmentInputHistory(t, reopenedPool, session.ID, 0, 0)
			later := reserveEnvironmentInput(t, reopened, tenant, session.ID, "later")
			if later.IsInitial {
				t.Fatal("later submission inferred initial origin")
			}
			requireEnvironmentInputActivity(t, reopened, tenant, session.ID, "idle", "")
			after, err := reopened.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil || len(after) < len(events) || !reflect.DeepEqual(events, after[:len(events)]) {
				t.Fatal("later work changed historical failure", err)
			}
			if err := reopened.DeleteSession(t.Context(), tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.GetSession(t.Context(), tenant, session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted initial Session remained visible", err)
			}
		})
	}
}
