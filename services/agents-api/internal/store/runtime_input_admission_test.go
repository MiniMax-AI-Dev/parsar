package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestManagedRuntimeDisabledAdmissionPreservesCancelAndRetry(t *testing.T) {
	s, _ := store.NewTestStore(t)
	tenant, session, _ := managedSession(t, s)
	inputs := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"accepted work"}`)}}
	accepted, err := s.SubmitInputs(t.Context(), tenant, session.ID, "work", inputs)
	if err != nil {
		t.Fatal(err)
	}
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, stop := managedWorker(t, s, uuid.NewString(), p)
	defer stop()
	if _, err := w.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: session.Configuration}); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("disabled admission accepted new hosted Session", err)
	}
	cancel := []store.Input{{Kind: "cancel", Payload: json.RawMessage(`{}`)}}
	first, err := w.SubmitInputs(t.Context(), tenant, session.ID, "cancel", cancel)
	if err != nil || len(first) != 1 {
		t.Fatal("existing work cannot be cancelled", first, err)
	}
	replay, err := w.SubmitInputs(t.Context(), tenant, session.ID, "cancel", cancel)
	if err != nil || len(replay) != 1 || !replay[0].Replayed || replay[0].Sequence != first[0].Sequence || replay[0].TurnID != first[0].TurnID {
		t.Fatal("cancel retry lost its receipt", replay, err)
	}
	retry, err := w.SubmitInputs(t.Context(), tenant, session.ID, "work", inputs)
	if err != nil || len(retry) != 1 || !retry[0].Replayed || retry[0].Sequence != accepted[0].Sequence || retry[0].TurnID != accepted[0].TurnID {
		t.Fatal("matching input retry lost its accepted outcome", retry, err)
	}
	if _, err := w.SubmitInputs(t.Context(), uuid.NewString(), session.ID, "cancel", cancel); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled admission weakened tenant isolation", err)
	}
	if p.creates != 0 {
		t.Fatal("existing controls provisioned a new Runtime")
	}
}
