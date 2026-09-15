package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestSelfHostedCancellationOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "cancel-caller", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: foreignTenant, SubjectKind: "service_account", SubjectID: "cancel-caller", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	serve := func() (*httptest.Server, func(bool)) {
		t.Helper()
		worker, stop := publicInitialWorker(t, s)
		handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker), api.WithEnvironmentRemoteURL("https://offline-executor.example"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server, stop
	}
	server, stop := serve()
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
		command := exec.CommandContext(ctx, python, "../../tests/official_self_hosted_cancel.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("public self-hosted cancellation %s: %v %s", phase, err, output)
		}
		if !json.Valid(output) {
			t.Fatal("invalid public cancellation fixture result")
		}
		return output
	}
	accepted := run("create")
	var created struct {
		ID        string `json:"id"`
		InitialID string `json:"initial_id"`
		LaterID   string `json:"later_id"`
		IdleKey   string `json:"idle_key"`
		ActiveKey string `json:"active_key"`
	}
	if err := json.Unmarshal(accepted, &created); err != nil || created.ID == "" || created.InitialID == "" || created.LaterID == "" {
		t.Fatal("missing public cancellation fixture identities", err)
	}
	settings["accepted"] = accepted
	receipts := func(key, target string) []store.InputReceipt {
		t.Helper()
		rows, err := pool.Query(t.Context(), `SELECT sequence, COALESCE(turn_id::text,'') FROM turn_inputs
			WHERE session_id=$1 AND idempotency_key=$2 ORDER BY batch_position`, created.ID, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []store.InputReceipt
		for rows.Next() {
			var receipt store.InputReceipt
			if err := rows.Scan(&receipt.Sequence, &receipt.TurnID); err != nil {
				t.Fatal(err)
			}
			if receipt.Sequence <= 0 || receipt.TurnID != target {
				t.Fatal("public cancellation receipt changed target")
			}
			result = append(result, receipt)
		}
		if err := rows.Err(); err != nil || len(result) != 2 || result[1].Sequence != result[0].Sequence+1 {
			t.Fatal("cancellation batch did not retain exactly two ordered receipts", err)
		}
		return result
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
	idleReceipts := receipts(created.IdleKey, "")
	later, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.LaterID, "controlled-later-input", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"Retain pending input."}`)}})
	if err != nil || later.State != store.EnvironmentInputPending || later.IsInitial {
		t.Fatal("could not establish controlled later reservation", err)
	}
	pending := map[string]string{created.InitialID: snapshot(created.InitialID), created.LaterID: snapshot(created.LaterID)}
	transition := func(id, from, to string) {
		t.Helper()
		if _, err := s.TransitionTurn(t.Context(), tenant, created.ID, id, store.TurnTransition{ExpectedStatus: from, Status: to}); err != nil {
			t.Fatal(err)
		}
	}
	start := func() string {
		t.Helper()
		// Controlled callbacks isolate HTTP admission; no daemon or model runs in this fixture.
		input, err := s.SubmitMessage(t.Context(), tenant, created.ID, uuid.NewString(), json.RawMessage(`{"text":"Controlled active work."}`))
		if err != nil {
			t.Fatal(err)
		}
		transition(input.TurnID, store.TurnQueued, store.TurnInProgress)
		settings["turn_id"] = input.TurnID
		return input.TurnID
	}
	first := start()
	if err := s.AppendTurnEvents(t.Context(), tenant, created.ID, first, 1, []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"item_id":"controlled-partial","delta":"Retained partial output."}`)}}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(created.ID)
	run("idle_replay")
	if snapshot(created.ID) != before {
		t.Fatal("idle cancellation replay or rejected input changed active work")
	}
	itemsBefore, err := s.ListItems(t.Context(), tenant, created.ID, "", 100, true)
	if err != nil || len(itemsBefore.Items) != 2 {
		t.Fatal("controlled partial output was not recorded", err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	run("active")
	activeReceipts := receipts(created.ActiveKey, first)
	turn, err := s.GetTurn(t.Context(), tenant, created.ID, first)
	if err != nil || turn.Status != store.TurnInProgress || turn.CancelRequestedAt.IsZero() || !turn.CompletedAt.IsZero() {
		t.Fatal("204 must admit cancellation without fabricating native completion", err)
	}
	itemsAfter, err := s.ListItems(t.Context(), tenant, created.ID, "", 100, true)
	if err != nil || !reflect.DeepEqual(itemsBefore, itemsAfter) {
		t.Fatal("cancellation admission changed partial history", err)
	}
	afterCursor, err := s.SessionEventCursor(t.Context(), tenant, created.ID)
	if err != nil || afterCursor != cursor {
		t.Fatal("cancellation admission fabricated an execution event", err)
	}
	transition(first, store.TurnInProgress, store.TurnCancelled)
	for _, reopen := range []bool{false, true} {
		if reopen {
			stop(false)
			server.Close()
			pool.Close()
			s, pool = store.NewTestStore(t)
			server, stop = serve()
			settings["base"] = server.URL
		}
		next := start()
		before := snapshot(created.ID)
		run("replay")
		if snapshot(created.ID) != before || !reflect.DeepEqual(idleReceipts, receipts(created.IdleKey, "")) || !reflect.DeepEqual(activeReceipts, receipts(created.ActiveKey, first)) {
			t.Fatal("cancel replay changed original identity or later active work", "reopened", reopen)
		}
		for id, expected := range pending {
			if snapshot(id) != expected {
				t.Fatal("rejected cancellation changed pending reservation, deadline or history", "reopened", reopen)
			}
		}
		transition(next, store.TurnInProgress, store.TurnCompleted)
	}
}
