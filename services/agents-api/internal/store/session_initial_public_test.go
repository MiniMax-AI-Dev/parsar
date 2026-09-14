package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestInitialSessionInputOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, _ := store.NewTestStore(t)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()}, {OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise real worker admission with dispatch paused for deterministic reads.
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopped, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(stopped); err != context.Canceled {
			t.Error(err)
		}
	})
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	unsupported, err := api.NewHandler(s, auth, "claude_code", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(unsupported)
	defer other.Close()
	command := exec.CommandContext(t.Context(), python, "../../tests/official_session_initial_input.py", server.URL, token, foreign, other.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official initial input: %v %s", err, output)
	}
}
