package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func requireEnvironmentInputActivity(t *testing.T, s *Store, tenant, session, status, environment string) Session {
	t.Helper()
	value, err := s.GetSession(t.Context(), tenant, session)
	if err != nil {
		t.Fatal(err)
	}
	activity := value.EnvironmentInputActivity
	if status == "" {
		if activity != nil {
			t.Fatal("unexpected input activity", activity)
		}
	} else if activity == nil || activity.Status != status || activity.EnvironmentID != environment || activity.LastActiveAt.IsZero() {
		t.Fatal("input activity", activity, status, environment)
	}
	page, err := s.ListSessions(t.Context(), tenant, "", 100, false, nil)
	if err != nil || len(page.Sessions) != 1 || !reflect.DeepEqual(page.Sessions[0].EnvironmentInputActivity, activity) {
		t.Fatal("list and retrieve activity differ", err)
	}
	return value
}

func TestEnvironmentInputActivityWaitsBeforeTurnAndClearsOnConnection(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := environmentInputSession(t, s)
	value := requireEnvironmentInputActivity(t, s, tenant, session.ID, "", "")
	if value.Environment == nil || value.Environment.Status != "pending" || value.LastTurn != nil {
		t.Fatal("idle Environment projection", value)
	}
	environment := value.Environment.ID
	reservation := reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
	waiting := requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", environment)
	if waiting.LastTurn != nil || !waiting.EnvironmentInputActivity.LastActiveAt.Equal(reservation.CreatedAt) {
		t.Fatal("waiting input fabricated a Turn")
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	first, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil || len(first) != 1 || first[0].Event.Type != "agent.session.requires_action" || first[0].Turn != nil || first[0].EnvironmentInputActivity == nil {
		t.Fatal("missing pre-Turn snapshot", first, err)
	}
	reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
	writer := executionLease(t, s).Store()
	generation := uuid.NewString()
	if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment, generation); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	idle := requireEnvironmentInputActivity(t, s, tenant, session.ID, "idle", "")
	if idle.LastTurn != nil {
		t.Fatal("connection fabricated readiness or Turn")
	}
	changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil || len(changes) != 3 || changes[1].Event.Type != "agent.session.environment.connected" || changes[2].Event.Type != "agent.session.idle" {
		t.Fatal("connection/action order", changes, err)
	}
	if !reflect.DeepEqual(first[0], changes[0]) || changes[2].Turn != nil || changes[2].EnvironmentInputActivity.Status != "idle" {
		t.Fatal("activity snapshot changed or borrowed a Turn")
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 2, false); err != nil {
		t.Fatal(err)
	}
	requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", environment)
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 3, true); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID); err != nil {
		t.Fatal(err)
	}
	active := requireEnvironmentInputActivity(t, s, tenant, session.ID, "", "")
	if active.LastTurn == nil || active.LastTurn.Status != TurnInProgress {
		t.Fatal("normal Turn did not take ownership")
	}
	if _, err := s.GetSession(t.Context(), uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign Session activity visible", err)
	}
}

func TestEnvironmentInputActivitySettlementAndNewerWork(t *testing.T) {
	for _, state := range []string{EnvironmentInputCancelled, EnvironmentInputExpired} {
		t.Run(state, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session := environmentInputSession(t, s)
			writer := executionLease(t, s).Store()
			prior, err := s.SubmitInputs(t.Context(), tenant, session.ID, "prior", []Input{messageInput("prior")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.TransitionTurn(t.Context(), tenant, session.ID, prior[0].TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnFailed, Outcome: []byte(`{}`)}); err != nil {
				t.Fatal(err)
			}
			reservation := reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
			value, err := s.GetSession(t.Context(), tenant, session.ID)
			if err != nil || value.LastTurn.Status != TurnFailed || value.EnvironmentInputActivity.Status != "requires_action" {
				t.Fatal("prior failure hid waiting input", err)
			}
			if state == EnvironmentInputCancelled {
				_, err = s.CancelEnvironmentInput(t.Context(), tenant, session.ID, reservation.ID)
			} else {
				if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", reservation.ID); err != nil {
					t.Fatal(err)
				}
				// Other retained test rows may precede this reservation in bounded batches.
				for reservation.State == EnvironmentInputPending {
					count, sweepErr := writer.ExpireEnvironmentInputs(t.Context())
					if sweepErr != nil || count < 1 || count > 32 {
						t.Fatal("expiry made no bounded progress", count, sweepErr)
					}
					reservation, err = s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, reservation.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			requireEnvironmentInputActivity(t, s, tenant, session.ID, "idle", "")
			cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
			if after, err := s.SessionEventCursor(t.Context(), tenant, session.ID); err != nil || after != cursor {
				t.Fatal("settled retry repeated activity", after, cursor, err)
			}
			if _, err := s.SubmitInputs(t.Context(), tenant, session.ID, "newer", []Input{messageInput("newer")}); err != nil {
				t.Fatal(err)
			}
			requireEnvironmentInputActivity(t, s, tenant, session.ID, "", "")
		})
	}
}

func TestEnvironmentInputActivityRollsBackReservationAndConnection(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := environmentInputSession(t, s)
	constraint := pgx.Identifier{"input_activity_" + uuid.NewString()[:8]}.Sanitize()
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (session_id <> '"+session.ID+"' OR NOT (payload ? 'environment_input_activity')) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE session_events DROP CONSTRAINT IF EXISTS "+constraint)
	})
	if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "rollback", []Input{messageInput("pending")}); err == nil {
		t.Fatal("activity failure retained reservation")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1", session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial reservation", count, err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
	value := requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", session.Environment.ID)
	writer := executionLease(t, s).Store()
	generation := uuid.NewString()
	if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, value.Environment.ID, generation); err != nil {
		t.Fatal(err)
	}
	before := connectionSnapshot(t, pool, value.Environment.ID)
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (session_id <> '"+session.ID+"' OR payload->'event'->>'type' <> 'agent.session.idle') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, value.Environment.ID, generation, 1, true); err == nil {
		t.Fatal("connection committed without activity")
	}
	if connection := connectionSnapshot(t, pool, value.Environment.ID); connection != before {
		t.Fatal("partial connection state", connection)
	}
	requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", value.Environment.ID)
}

func TestEnvironmentInputActivityRecoversWaitingActionAndHidesDeletion(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := environmentInputSession(t, s)
	reservation := reserveEnvironmentInput(t, s, tenant, session.ID, "waiting")
	old := executionLease(t, s)
	generation := uuid.NewString()
	environment := session.Environment.ID
	if err := old.Store().ReplaceEnvironmentConnection(t.Context(), tenant, environment, generation); err != nil {
		t.Fatal(err)
	}
	if err := old.Store().ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	requireEnvironmentInputActivity(t, s, tenant, session.ID, "idle", "")
	if err := old.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := executionLease(t, s).Store()
	if err := next.ReconcileEnvironmentConnections(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireEnvironmentInputActivity(t, s, tenant, session.ID, "requires_action", environment)
	got, err := s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, reservation.ID)
	if err != nil || got.State != EnvironmentInputPending || !got.Deadline.Equal(reservation.Deadline) {
		t.Fatal("recovery changed waiting input or its deadline", got, err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 2, true); err != nil {
		t.Fatal(err)
	}
	if after, err := s.SessionEventCursor(t.Context(), tenant, session.ID); err != nil || after != cursor {
		t.Fatal("retired generation changed activity", after, cursor, err)
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(t.Context(), tenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted activity remained visible", err)
	}
	if _, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted activity events remained visible", err)
	}
}
