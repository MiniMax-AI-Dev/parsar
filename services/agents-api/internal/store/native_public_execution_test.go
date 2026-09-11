package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/google/uuid"
)

func verifyNativePublicExecution(t *testing.T, h *dispatchHarness, parent context.Context, evidence string) {
	t.Helper()
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Log("Public SDK proof requires PARSAR_OFFICIAL_SDK_PYTHON")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{TokenSHA256: device.HashCredential(token), TenantID: h.tenant}, {TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, "codex", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	command := exec.CommandContext(ctx, python, "../../tests/official_execution.py", server.URL, token, foreign, filepath.Join(evidence, "public-execution.json"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official SDK native execution: %v\n%s", err, output)
	}
	t.Logf("Official SDK public execution, retry identity, isolation, result recovery and native cancellation passed: %s", evidence)
}
