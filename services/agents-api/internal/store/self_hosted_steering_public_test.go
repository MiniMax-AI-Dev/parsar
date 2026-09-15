package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestSelfHostedSteeringOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "steering-caller", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "steering-caller", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := publicInitialWorker(t, s)
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker), api.WithEnvironmentRemoteURL("https://offline-executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	settings := map[string]any{"base": server.URL, "token": token, "foreign_token": foreign}
	run := func(phase string) json.RawMessage {
		t.Helper()
		settings["phase"] = phase
		input, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, python, "../../tests/official_self_hosted_steering.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("public self-hosted steering %s: %v %s", phase, err, output)
		}
		if !json.Valid(output) {
			t.Fatal("invalid public steering fixture result")
		}
		return output
	}
	accepted := run("create")
	var created struct {
		ID           string            `json:"id"`
		InitialID    string            `json:"initial_id"`
		LaterID      string            `json:"later_id"`
		BatchKey     string            `json:"batch_key"`
		PendingKey   string            `json:"pending_key"`
		IdleKey      string            `json:"idle_key"`
		RollbackKey  string            `json:"rollback_key"`
		Batch        []json.RawMessage `json:"batch"`
		PendingEvent json.RawMessage   `json:"pending_event"`
	}
	if err := json.Unmarshal(accepted, &created); err != nil || created.ID == "" || len(created.Batch) != 2 {
		t.Fatal("missing public steering fixture identities", err)
	}
	settings["accepted"] = accepted
	inputs := []store.Input{{Kind: "message", Payload: created.Batch[0]}, {Kind: "message", Payload: created.Batch[1]}}
	if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.LaterID, created.PendingKey, []store.Input{{Kind: "message", Payload: created.PendingEvent}}); err != nil {
		t.Fatal(err)
	}
	snapshot := func(sessionID string) string {
		t.Helper()
		var value string
		err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
			'session', (SELECT to_jsonb(s) FROM sessions s WHERE id=$1),
			'reservations', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM environment_input_reservations r WHERE session_id=$1),
			'turns', (SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM turns t WHERE session_id=$1),
			'inputs', (SELECT jsonb_agg(to_jsonb(i) ORDER BY i.sequence) FROM turn_inputs i WHERE session_id=$1),
			'items', (SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM session_items i WHERE session_id=$1),
			'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.sequence) FROM session_events e WHERE session_id=$1))::text`, sessionID).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	pending := map[string]string{created.InitialID: snapshot(created.InitialID), created.LaterID: snapshot(created.LaterID)}
	transition := func(turn, from, to string) {
		t.Helper()
		if _, err := s.TransitionTurn(t.Context(), tenant, created.ID, turn, store.TurnTransition{ExpectedStatus: from, Status: to}); err != nil {
			t.Fatal(err)
		}
	}
	start := func() string {
		t.Helper()
		// Controlled Turn callbacks isolate admission from native application and model behavior.
		input, err := s.SubmitMessage(t.Context(), tenant, created.ID, uuid.NewString(), json.RawMessage(`{"text":"Controlled original work."}`))
		if err != nil {
			t.Fatal(err)
		}
		transition(input.TurnID, store.TurnQueued, store.TurnInProgress)
		settings["turn_id"] = input.TurnID
		return input.TurnID
	}
	first := start()
	run("active")
	direct, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.ID, created.BatchKey, inputs)
	if err != nil || direct.State != store.EnvironmentInputAdmitted || direct.ID != "" || !direct.Deadline.IsZero() || len(direct.Receipts) != 2 || direct.Receipts[0].TurnID != first || !direct.Receipts[0].Replayed {
		t.Fatal("active batch acquired a reservation or changed its direct receipt", direct, err)
	}
	history, err := s.ListTurnInputs(t.Context(), tenant, created.ID, first, 0, 100)
	if err != nil || len(history) != 3 || history[1].Sequence != direct.Receipts[0].Sequence || history[2].Sequence != direct.Receipts[1].Sequence {
		t.Fatal("concurrent active batch duplicated or reordered inputs", err)
	}
	var reservations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1", created.ID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatal("active text created a preparation reservation", err)
	}
	before := snapshot(created.ID)
	run("reject")
	if snapshot(created.ID) != before {
		t.Fatal("rejected steering changed history or retry identity")
	}
	constraint := "steering_failure_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(t.Context(), "ALTER TABLE turn_inputs ADD CONSTRAINT "+constraint+" CHECK (idempotency_key <> '"+created.RollbackKey+"' OR batch_position=0)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE turn_inputs DROP CONSTRAINT IF EXISTS "+constraint)
	})
	run("rollback")
	if snapshot(created.ID) != before {
		t.Fatal("second-insert failure left partial text, Items or events")
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE turn_inputs DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	transition(first, store.TurnInProgress, store.TurnCompleted)
	for _, phase := range []string{"terminal", "later"} {
		if phase == "later" {
			start()
		}
		before := snapshot(created.ID)
		run(phase)
		retry, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.ID, created.BatchKey, inputs)
		if err != nil || !reflect.DeepEqual(direct, retry) || snapshot(created.ID) != before {
			t.Fatal("active text retry retargeted work or changed its direct identity", err)
		}
	}
	transition(settings["turn_id"].(string), store.TurnInProgress, store.TurnCompleted)
	run("idle")
	var id string
	if err := pool.QueryRow(t.Context(), "SELECT id FROM environment_input_reservations WHERE session_id=$1 AND idempotency_key=$2", created.ID, created.IdleKey).Scan(&id); err != nil {
		t.Fatal("idle input did not wait for preparation", err)
	}
	idle, err := s.GetEnvironmentInputReservation(t.Context(), tenant, created.ID, id)
	if err != nil || idle.State != store.EnvironmentInputPending || len(idle.Receipts) != 0 || idle.Deadline.Sub(idle.CreatedAt) != 5*time.Minute {
		t.Fatal("idle input bypassed preparation", idle, err)
	}
	for id, expected := range pending {
		if snapshot(id) != expected {
			t.Fatal("steering changed pending input, deadline or foreign history")
		}
	}
}
