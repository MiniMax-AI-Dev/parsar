package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func environmentInput(key, kind, directory string) CreateSessionInput {
	configuration, _ := json.Marshal(map[string]any{
		"agent":       map[string]string{"model": "fixture-model"},
		"environment": map[string]any{"type": kind, "workspace_directory": directory, "capability_directories": []string{}},
	})
	return CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: key, Configuration: configuration}
}

func TestEnvironmentOwnershipPersistsAndStaysScoped(t *testing.T) {
	for _, kind := range []string{"self_hosted", "openai_hosted"} {
		t.Run(kind, func(t *testing.T) {
			s, pool := testStore(t)
			ctx := context.Background()
			tenant, foreign := uuid.NewString(), uuid.NewString()
			input := environmentInput("environment", kind, "/workspace")
			session, err := s.CreateSession(ctx, tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			first, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
			if err != nil || first.ID == "" || first.ID == session.ID || first.SessionID != session.ID || first.TenantID != tenant || first.Status != "pending" || first.CreatedAt.IsZero() {
				t.Fatal(first, err)
			}
			got, err := s.GetEnvironment(ctx, tenant, first.ID)
			if err != nil || !reflect.DeepEqual(got, first) {
				t.Fatal(got, err)
			}
			for _, lookup := range []func() error{
				func() error { _, err := s.GetEnvironment(ctx, foreign, first.ID); return err },
				func() error { _, err := s.GetSessionEnvironment(ctx, foreign, session.ID); return err },
			} {
				if err := lookup(); !errors.Is(err, ErrNotFound) {
					t.Fatal("foreign access", err)
				}
			}
			other, err := s.CreateSession(ctx, foreign, input)
			if err != nil {
				t.Fatal(err)
			}
			otherEnvironment, err := s.GetSessionEnvironment(ctx, foreign, other.ID)
			if err != nil || otherEnvironment.ID == first.ID {
				t.Fatal(otherEnvironment, err)
			}
			changed := environmentInput(input.IdempotencyKey, kind, "/changed")
			if _, err := s.CreateSession(ctx, tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("changed configuration accepted", err)
			}
			if _, err := s.UpdateSessionMetadata(ctx, tenant, session.ID, map[string]string{"updated": "yes"}); err != nil {
				t.Fatal(err)
			}
			pool.Close()
			restarted, _ := testStore(t)
			retry, err := restarted.CreateSession(ctx, tenant, input)
			if err != nil || retry.ID != session.ID || retry.Metadata["updated"] != "yes" {
				t.Fatal(retry, err)
			}
			retained, err := restarted.GetSessionEnvironment(ctx, tenant, retry.ID)
			if err != nil || !reflect.DeepEqual(retained, first) {
				t.Fatal(retained, err)
			}
		})
	}
}

func TestEnvironmentCreationWinnerOwnsSnapshotAndIdentity(t *testing.T) {
	s, pool := testStore(t)
	other, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	intent := json.RawMessage(`{"request":"resolved-template"}`)
	const count = 8
	type result struct {
		creation    SessionCreation
		environment Environment
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			if i%2 != 0 {
				st = other
			}
			input := environmentInput("winner", "openai_hosted", fmt.Sprintf("/workspace/%d", i))
			input.CreationRequest = intent
			input.InitialInputs = []Input{messageInput("initial")}
			creation, err := st.CreateSessionStream(ctx, tenant, input)
			if err != nil {
				t.Error(err)
				return
			}
			environment, err := st.GetSessionEnvironment(ctx, tenant, creation.Session.ID)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result{creation, environment}
		}()
	}
	wg.Wait()
	close(results)
	var first result
	created, received := 0, 0
	for got := range results {
		received++
		if first.environment.ID == "" {
			first = got
		}
		if got.creation.Created {
			created++
		}
		if got.creation.Session.ID != first.creation.Session.ID || !reflect.DeepEqual(got.environment, first.environment) {
			t.Fatal("concurrent retry changed ownership", got)
		}
		var snapshot struct {
			Environment json.RawMessage `json:"environment"`
		}
		if err := json.Unmarshal(got.creation.Session.Configuration, &snapshot); err != nil {
			t.Fatal(err)
		}
		canonical, err := canonicalJSONObject(snapshot.Environment)
		if err != nil || string(canonical) != string(got.environment.Configuration) {
			t.Fatal("configuration diverged", err)
		}
	}
	if received != count || created != 1 {
		t.Fatal("creation winners", received, created)
	}
	var associations, reservations int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM environments WHERE session_id=$1", first.creation.Session.ID).Scan(&associations); err != nil || associations != 1 {
		t.Fatal(associations, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1 AND is_initial", first.creation.Session.ID).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatal(reservations, err)
	}
	environmentInputHistory(t, pool, first.creation.Session.ID, 0, 0)
	events, err := s.ListSessionEvents(ctx, tenant, first.creation.Session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	restarted, _ := testStore(t)
	retryInput := environmentInput("winner", "openai_hosted", "/changed-resolution")
	retryInput.CreationRequest = intent
	retryInput.InitialInputs = []Input{messageInput("initial")}
	retry, err := restarted.CreateSessionStream(ctx, tenant, retryInput)
	if err != nil || retry.Created || retry.Session.ID != first.creation.Session.ID {
		t.Fatal(retry, err)
	}
	found, err := restarted.FindSessionCreation(ctx, tenant, "winner", intent, FixtureCreator())
	if err != nil || found.Created || found.Session.ID != first.creation.Session.ID {
		t.Fatal(found, err)
	}
	environment, err := restarted.GetSessionEnvironment(ctx, tenant, found.Session.ID)
	if err != nil || !reflect.DeepEqual(environment, first.environment) {
		t.Fatal(environment, err)
	}
	after, err := restarted.ListSessionEvents(ctx, tenant, found.Session.ID, 0)
	if err != nil || !reflect.DeepEqual(events, after) {
		t.Fatal("retry emitted work", err)
	}
}

func TestEnvironmentCreationFailureRollsBackAllResources(t *testing.T) {
	for _, phase := range []string{"environment", "input", "activity"} {
		t.Run(phase, func(t *testing.T) {
			s, pool := testStore(t)
			ctx := context.Background()
			tenant, marker := uuid.NewString(), uuid.NewString()
			constraint := "environment_failure_" + strings.ReplaceAll(marker, "-", "")
			table, expression := "environments", "status <> 'pending'"
			if phase == "input" {
				table, expression = "environment_input_reservations", "NOT (batch @> '[{\"payload\":{\"text\":\""+marker+"\"}}]'::jsonb)"
			}
			if phase == "activity" {
				table, expression = "session_events", "NOT (payload ? 'environment_input_activity')"
			}
			var before int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM environments").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "ALTER TABLE "+table+" ADD CONSTRAINT "+constraint+" CHECK ("+expression+") NOT VALID"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, "ALTER TABLE "+table+" DROP CONSTRAINT IF EXISTS "+constraint) })
			input := environmentInput("rollback", "self_hosted", "/workspace")
			input.InitialInputs = []Input{messageInput("first"), messageInput(marker)}
			if got, err := s.CreateSession(ctx, tenant, input); err == nil || got.ID != "" {
				t.Fatal("partial creation succeeded", got, err)
			}
			var sessions, after int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&sessions); err != nil || sessions != 0 {
				t.Fatal("partial Session survived", sessions, err)
			}
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM environments").Scan(&after); err != nil || after != before {
				t.Fatal("partial Environment survived", after, err)
			}
			if _, err := pool.Exec(ctx, "ALTER TABLE "+table+" DROP CONSTRAINT "+constraint); err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(ctx, tenant, input)
			if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity == nil || session.EnvironmentInputActivity.Status != "requires_action" {
				t.Fatal("retry remained reserved", session, err)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
			if environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID); err != nil || environment.ID == "" {
				t.Fatal(environment, err)
			}
		})
	}
}

func TestEnvironmentDeletionHidesWithoutDestroyingOwnership(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := environmentInput("deleted", "self_hosted", "/workspace")
	session, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEnvironment(ctx, tenant, environment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.GetSessionEnvironment(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("deleted retry resurrected ownership", err)
	}
	var retained int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM environments WHERE id=$1 AND session_id=$2", environment.ID, session.ID).Scan(&retained); err != nil || retained != 1 {
		t.Fatal(retained, err)
	}
}

func TestEnvironmentAbsentForNoneAndLegacySnapshots(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	for i, configuration := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"environment":{"type":"none"}}`)} {
		input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: fmt.Sprintf("none-%d", i), Configuration: configuration}
		session, err := s.CreateSession(ctx, tenant, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSessionEnvironment(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("unexpected Environment", err)
		}
		retry, err := s.CreateSession(ctx, tenant, input)
		if err != nil || retry.ID != session.ID {
			t.Fatal(retry, err)
		}
	}
}
