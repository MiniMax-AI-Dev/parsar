package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestFunctionInputsOfficialClientAtomicAdmission(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("PARSAR_OFFICIAL_SDK_PYTHON is required for official-client verification")
	}
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "rollback", "late"} {
		if err := s.RecordFunctionCall(ctx, tenant, session.ID, input.TurnID, store.FunctionCall{CallID: id, ExecutorCallID: "native-" + id, Name: "lookup", Arguments: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := api.NewAuthenticator([]api.APIKey{{TokenSHA256: device.HashCredential(token), TenantID: tenant}, {TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	command := exec.CommandContext(ctx, python, "../../tests/official_function_inputs.py", server.URL, token, foreign, session.ID, input.TurnID, other.ID)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = command.Wait() }()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "saved\n" {
		t.Fatalf("SDK admission: %s %v %s", line, err, stderr.String())
	}
	for _, id := range []string{"a", "b", "c", "rollback", "late"} {
		call, err := s.GetFunctionCall(ctx, tenant, session.ID, input.TurnID, id)
		if err != nil || call.Applied {
			t.Fatal(call, err)
		}
		var result map[string]json.RawMessage
		if call.Result != nil {
			if err := json.Unmarshal(call.Result, &result); err != nil {
				t.Fatal(err)
			}
		}
		switch id {
		case "a":
			var parts []map[string]any
			_ = json.Unmarshal(result["output"], &parts)
			if string(result["success"]) != "false" || string(result["error"]) != `"failure"` || len(parts) != 3 || parts[0]["text"] != "" || parts[1]["image_url"] != "data:image/png;base64,AA==" || parts[2]["text"] != "last" {
				t.Fatal(result)
			}
		case "b":
			if string(result["output"]) != "null" || string(result["error"]) != "null" {
				t.Fatal(result)
			}
		case "c":
			if len(result) != 1 || string(result["success"]) != "true" {
				t.Fatal(result)
			}
		default:
			if call.Result != nil {
				t.Fatal("failed batch saved a result", call)
			}
		}
	}
	history, err := s.ListTurnInputs(ctx, tenant, session.ID, input.TurnID, 0, 100)
	if err != nil || len(history) != 6 {
		t.Fatal(history, err)
	}
	if _, err := s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnWaiting, Status: store.TurnFailed}); err != nil {
		t.Fatal(err)
	}
	next, err := s.SubmitMessage(ctx, tenant, session.ID, "next", json.RawMessage(`{"text":"next"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write([]byte("terminal\n")); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("SDK retry: %v %s", err, stderr.String())
	}
	history, err = s.ListTurnInputs(ctx, tenant, session.ID, next.TurnID, 0, 100)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	current, err := s.GetTurn(ctx, tenant, session.ID, next.TurnID)
	if err != nil || current.Status != store.TurnQueued || !current.CancelRequestedAt.IsZero() {
		t.Fatal(current, err)
	}
}
