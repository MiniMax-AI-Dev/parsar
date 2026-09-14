package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSelfHostedCreationSnapshotRetainsEnvironmentAndCursor(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "environment-snapshot",
		Configuration:   json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
		CreationRequest: json.RawMessage(`{"agent_id":"saved-agent","environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
	}
	created, err := s.CreateSessionStream(t.Context(), tenant, input)
	if err != nil || !created.Created || created.Cursor != 0 || created.Session.Environment == nil {
		t.Fatal("missing creation Environment", created, err)
	}
	environment := created.Session.Environment
	if environment.ID == "" || environment.SessionID != created.Session.ID || environment.TenantID != tenant || environment.Status != "pending" {
		t.Fatal("incorrect creation association", environment)
	}
	pending, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.Session.ID, "later", []Input{messageInput("later")})
	if err != nil || pending.State != EnvironmentInputPending {
		t.Fatal(pending, err)
	}
	events, err := s.ListSessionEvents(t.Context(), tenant, created.Session.ID, created.Cursor)
	if err != nil || len(events) != 1 || events[0].Event.Type != "agent.session.requires_action" {
		t.Fatal("creation cursor lost subsequent activity", events, err)
	}
	retry, err := s.CreateSessionStream(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.FindSessionCreation(t.Context(), tenant, input.IdempotencyKey, input.CreationRequest, input.Creator)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []SessionCreation{retry, recovered} {
		snapshot := value.Session
		if value.Created || value.Cursor != events[0].Sequence || snapshot.Environment == nil || snapshot.Environment.ID != environment.ID || string(snapshot.Environment.Configuration) != string(environment.Configuration) {
			t.Fatal("retry changed Environment or cursor", value)
		}
		if snapshot.LastTurn != nil || snapshot.EnvironmentInputActivity != nil || snapshot.Usage != nil {
			t.Fatal("creation snapshot borrowed later activity", snapshot)
		}
	}
	changedCreator := input.Creator
	changedCreator.ID = "another-creator"
	if _, err := s.FindSessionCreation(t.Context(), tenant, input.IdempotencyKey, input.CreationRequest, changedCreator); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("retry creator isolation", err)
	}
	current, err := s.GetSession(t.Context(), tenant, created.Session.ID)
	if err != nil || current.EnvironmentInputActivity == nil || current.EnvironmentInputActivity.Status != "requires_action" {
		t.Fatal("ordinary read lost current activity", current, err)
	}
	if created.Cursor != 0 || created.Session.EnvironmentInputActivity != nil {
		t.Fatal("creation snapshot mutated")
	}
}
