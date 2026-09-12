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

func TestFunctionStateOfficialClientReadsAndLiveEvents(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("PARSAR_OFFICIAL_SDK_PYTHON is required for official-client verification")
	}
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cfg := json.RawMessage(`{"agent":{"id":"agent_fixture","model":"fixture","tools":[],"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"}},"environment":{"type":"none"}}`)
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "fixture", Configuration: cfg})
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
	record := func(id string) {
		t.Helper()
		if err := s.RecordFunctionCall(ctx, tenant, session.ID, input.TurnID, store.FunctionCall{CallID: id, ExecutorCallID: "private-" + id, Name: "lookup", Arguments: json.RawMessage(`{"ticket":9007199254740993}`)}); err != nil {
			t.Fatal(err)
		}
	}
	record("first")
	auth, err := api.NewAuthenticator([]api.APIKey{{TokenSHA256: device.HashCredential(token), TenantID: tenant}, {TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	command := exec.CommandContext(ctx, python, "../../tests/official_function_state.py", server.URL, token, foreign, session.ID, input.TurnID)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = command.Wait() }()
	reader := bufio.NewReader(stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "connected\n" {
		cancel()
		_ = command.Wait()
		t.Fatalf("SDK readiness: %s %v %s", line, err, stderr.String())
	}
	record("second")
	for _, id := range []string{"first", "second"} {
		if err := s.SubmitFunctionResult(ctx, tenant, session.ID, input.TurnID, id, json.RawMessage(`{"success":true,"output":"private"}`)); err != nil {
			t.Fatal(err)
		}
		if err := s.ConfirmFunctionResult(ctx, tenant, session.ID, input.TurnID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCompleted, nil, "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("official function-state verification: %v\n%s", err, stderr.String())
	}
	t.Log("Pinned official client verified persisted waiting reads, isolation, exact event fields and changing action snapshots")
}
