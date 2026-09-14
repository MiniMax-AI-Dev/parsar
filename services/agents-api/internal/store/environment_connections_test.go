package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func connectionFixture(t *testing.T, s *Store) (string, Session, Environment) {
	t.Helper()
	tenant, session := environmentInputSession(t, s)
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session, environment
}

func connectionSnapshot(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var value string
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object('environment',to_jsonb(e),'observation',to_jsonb(c),'sequence',s.event_sequence)::text
 FROM environments e JOIN sessions s ON s.id=e.session_id LEFT JOIN environment_connections c ON c.environment_id=e.id WHERE e.id=$1`, id).Scan(&value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func connectionChanges(t *testing.T, s *Store, tenant, session string) []SessionChange {
	t.Helper()
	all, err := s.ListSessionEvents(t.Context(), tenant, session, 0)
	if err != nil {
		t.Fatal(err)
	}
	var changes []SessionChange
	for _, change := range all {
		if change.Event.Environment != nil {
			changes = append(changes, change)
		}
	}
	return changes
}

func TestEnvironmentConnectionOrdersGenerationsAndImmutableEvents(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment := connectionFixture(t, s)
	writer := executionLease(t, s).Store()
	first, second := uuid.NewString(), uuid.NewString()
	replace := func(gen string) {
		t.Helper()
		if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, gen); err != nil {
			t.Fatal(err)
		}
	}
	observe := func(gen string, rev int64, connected bool) {
		t.Helper()
		if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, gen, rev, connected); err != nil {
			t.Fatal(err)
		}
	}
	replace(first)
	if len(connectionChanges(t, s, tenant, session.ID)) != 0 {
		t.Fatal("registration claimed a connection")
	}
	observe(first, 1, true)
	before := connectionSnapshot(t, pool, environment.ID)
	replace(first)
	observe(first, 1, true)
	if after := connectionSnapshot(t, pool, environment.ID); after != before {
		t.Fatal("retry changed a current observation")
	}
	observe(first, 3, false)
	observe(first, 2, true)
	observe(first, 4, true)
	replace(second)
	observe(second, 2, true)
	before = connectionSnapshot(t, pool, environment.ID)
	observe(first, 100, false)
	observe(second, 1, false)
	if after := connectionSnapshot(t, pool, environment.ID); after != before {
		t.Fatal("late observation overwrote a successor")
	}
	changes := connectionChanges(t, s, tenant, session.ID)
	want := []string{"connected", "disconnected", "connected", "disconnected", "connected"}
	var got []string
	for _, change := range changes {
		event := change.Event
		got = append(got, event.Environment.Status)
		if event.SessionID != session.ID || event.Environment.ID != environment.ID || event.Environment.Type != "self_hosted" || event.Type != "agent.session.environment."+event.Environment.Status || event.EventID == "" || event.TurnID != "" {
			t.Fatal("incorrect event identity", event)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire) != 4 {
			t.Fatal("private observation fields escaped", string(raw))
		}
		var state map[string]json.RawMessage
		if err := json.Unmarshal(wire["environment"], &state); err != nil {
			t.Fatal(err)
		}
		if len(state) != 4 || string(state["error"]) != "null" || strings.Contains(string(raw), first) || strings.Contains(string(raw), second) {
			t.Fatal("unsafe or incomplete state snapshot", string(raw))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	retained, err := s.GetEnvironment(t.Context(), tenant, environment.ID)
	if err != nil || retained.Status != "connected" {
		t.Fatal(retained, err)
	}
}

func TestEnvironmentConnectionRequiresOwnerAndRollsBackWithEvent(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment := connectionFixture(t, s)
	generation := uuid.NewString()
	if err := s.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err == nil {
		t.Fatal("unleased replacement accepted")
	}
	if err := s.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err == nil {
		t.Fatal("unleased observation accepted")
	}
	lease := executionLease(t, s)
	writer := lease.Store()
	if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err := writer.ReplaceEnvironmentConnection(t.Context(), uuid.NewString(), environment.ID, generation); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign generation accepted", err)
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, session.ID, generation, 1, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("Session ID used as Environment", err)
	}
	before := connectionSnapshot(t, pool, environment.ID)
	constraint := pgx.Identifier{"connection_event_" + uuid.NewString()[:8]}.Sanitize()
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events ADD CONSTRAINT "+constraint+" CHECK (payload->'event'->'environment'->>'id' IS DISTINCT FROM '"+environment.ID+"') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "ALTER TABLE session_events DROP CONSTRAINT IF EXISTS "+constraint)
		if err != nil {
			t.Error(err)
		}
	})
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err == nil {
		t.Fatal("event failure did not abort transaction")
	}
	if after := connectionSnapshot(t, pool, environment.ID); after != before {
		t.Fatal("event failure retained partial state/revision")
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE session_events DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	before = connectionSnapshot(t, pool, environment.ID)
	if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 2, false); err == nil {
		t.Fatal("closed owner used pooled writer")
	}
	if after := connectionSnapshot(t, pool, environment.ID); after != before {
		t.Fatal("closed owner changed observation")
	}
}

func TestEnvironmentConnectionDoesNotReviveDeletedOrTerminalResources(t *testing.T) {
	for _, status := range []string{"deleted", "expired", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session, environment := connectionFixture(t, s)
			writer := executionLease(t, s).Store()
			generation := uuid.NewString()
			if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err != nil {
				t.Fatal(err)
			}
			if status == "deleted" {
				if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(t.Context(), "UPDATE environments SET status=$2 WHERE id=$1", environment.ID, status); err != nil {
					t.Fatal(err)
				}
			}
			before := connectionSnapshot(t, pool, environment.ID)
			for _, operation := range []func() error{
				func() error {
					return writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, uuid.NewString())
				},
				func() error {
					return writer.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true)
				},
			} {
				if err := operation(); err == nil {
					t.Fatal("revived a terminal or deleted target")
				}
			}
			if after := connectionSnapshot(t, pool, environment.ID); after != before {
				t.Fatal("terminal observation changed")
			}
		})
	}
}
