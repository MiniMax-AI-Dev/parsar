package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestSelfHostedFunctionsOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "function-caller", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "function-caller", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
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
		command := exec.CommandContext(ctx, python, "../../tests/official_self_hosted_functions.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("public self-hosted functions %s: %v %s", phase, err, output)
		}
		if !json.Valid(output) {
			t.Fatal("invalid public function fixture result")
		}
		return output
	}
	accepted := run("create")
	var created struct {
		ID        string `json:"id"`
		SavedID   string `json:"saved_id"`
		InitialID string `json:"initial_id"`
		LaterID   string `json:"later_id"`
	}
	if err := json.Unmarshal(accepted, &created); err != nil || created.ID == "" || created.SavedID == "" || created.InitialID == "" || created.LaterID == "" {
		t.Fatal("missing public function fixture identities", err)
	}
	settings["accepted"] = accepted
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 4 {
		t.Fatal("unsupported function configuration persisted a Session", count, err)
	}
	if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, created.LaterID, "controlled-later-input", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"Retain pending input."}`)}}); err != nil {
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
			'calls', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.turn_id,c.call_id) FROM function_calls c WHERE session_id=$1),
			'items', (SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM session_items i WHERE session_id=$1),
			'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.sequence) FROM session_events e WHERE session_id=$1))::text`, sessionID).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	transition := func(session, turn, from, to string) {
		t.Helper()
		if _, err := s.TransitionTurn(t.Context(), tenant, session, turn, store.TurnTransition{ExpectedStatus: from, Status: to}); err != nil {
			t.Fatal(err)
		}
	}
	start := func(session string, nativeCalls []string) (string, []string) {
		t.Helper()
		// Calls and observations are controlled callbacks, not daemon or model execution.
		input, err := s.SubmitMessage(t.Context(), tenant, session, uuid.NewString(), json.RawMessage(`{"text":"Controlled function work."}`))
		if err != nil {
			t.Fatal(err)
		}
		transition(session, input.TurnID, store.TurnQueued, store.TurnInProgress)
		var calls []string
		for _, native := range nativeCalls {
			id := items.Identity(input.TurnID, "tool:"+native)
			if err := s.RecordFunctionCall(t.Context(), tenant, session, input.TurnID, store.FunctionCall{CallID: id, ExecutorCallID: native, Name: "lookup_ticket", Arguments: json.RawMessage(`{"ticket":"42"}`)}); err != nil {
				t.Fatal(err)
			}
			calls = append(calls, id)
		}
		return input.TurnID, calls
	}
	first, calls := start(created.ID, []string{"a", "b", "c"})
	other, otherCalls := start(created.SavedID, []string{"other"})
	settings["turn_id"], settings["calls"] = first, calls
	settings["other_turn"], settings["other_call"] = other, otherCalls[0]
	unchanged := map[string]string{created.SavedID: snapshot(created.SavedID), created.InitialID: snapshot(created.InitialID), created.LaterID: snapshot(created.LaterID)}
	before := snapshot(created.ID)
	run("reject")
	if snapshot(created.ID) != before {
		t.Fatal("rejected result batch changed calls, receipts, activity or history")
	}
	submitted := run("submit")
	var result struct {
		Batch []map[string]any `json:"batch"`
	}
	if err := json.Unmarshal(submitted, &result); err != nil || len(result.Batch) != 3 {
		t.Fatal("missing submitted result batch", err)
	}
	settings["accepted"] = submitted
	history, err := s.ListTurnInputs(t.Context(), tenant, created.ID, first, 0, 100)
	if err != nil || len(history) != 4 || history[0].Kind != "message" {
		t.Fatal("same-key concurrency duplicated result receipts", err)
	}
	for index, callID := range calls {
		call, err := s.GetFunctionCall(t.Context(), tenant, created.ID, first, callID)
		if err != nil || call.Applied {
			t.Fatal("HTTP admission invented native application", err)
		}
		expected := result.Batch[index]
		delete(expected, "type")
		delete(expected, "turn_id")
		delete(expected, "call_id")
		var actual map[string]any
		if err := json.Unmarshal(call.Result, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("result omission, null, output order or error changed", err)
		}
		var input store.FunctionResultInput
		if err := json.Unmarshal(history[index+1].Payload, &input); err != nil || input.CallID != callID || input.TurnID != first || history[index+1].Kind != "tool_result" {
			t.Fatal("result batch order or target changed", err)
		}
		if err := s.ConfirmFunctionResult(t.Context(), tenant, created.ID, first, callID); err != nil {
			t.Fatal(err)
		}
	}
	var observations []store.ExecutionEvent
	for _, native := range []string{"a", "b", "c"} {
		observations = append(observations, store.ExecutionEvent{Kind: "tool_call", Payload: json.RawMessage(fmt.Sprintf(`{"id":%q,"stage":"after","observation":{"status":"completed","kind":"function","name":"lookup_ticket","arguments":{"ticket":"42"},"content":[{"type":"input_text","text":"normalized native output"}]}}`, native))})
	}
	if err := s.AppendTurnEvents(t.Context(), tenant, created.ID, first, 1, observations); err != nil {
		t.Fatal(err)
	}
	transition(created.ID, first, store.TurnInProgress, store.TurnCompleted)
	before = snapshot(created.ID)
	run("terminal")
	if snapshot(created.ID) != before {
		t.Fatal("terminal result retry changed the original receipt or history")
	}
	next, nextCalls := start(created.ID, []string{"next"})
	settings["next_turn"], settings["next_call"] = next, nextCalls[0]
	before = snapshot(created.ID)
	run("later")
	if snapshot(created.ID) != before {
		t.Fatal("prior result retry changed later work or old receipts")
	}
	for id, expected := range unchanged {
		if snapshot(id) != expected {
			t.Fatal("result submission changed another Session or pending reservation")
		}
	}
	transition(created.ID, next, store.TurnWaiting, store.TurnFailed)
	transition(created.SavedID, other, store.TurnWaiting, store.TurnFailed)
}
