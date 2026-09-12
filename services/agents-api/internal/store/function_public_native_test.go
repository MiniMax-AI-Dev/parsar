package store_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativePublicFunctionExecution(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	h, ctx, home := nativeDispatchHarness(t)
	model, output, requests := nativeFunctionModel(t, home)
	defer model.Close()
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"enable_features": []any{"multi_agent", "multi_agent_v2"}, "codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-test-token"}}, nil
	}
	serverURL, token := nativePublicFunctionServer(t, h, ctx)
	outputPath, proofPath := filepath.Join(home, "function-output.json"), filepath.Join(home, "public-functions.json")
	raw, _ := json.Marshal(output)
	if err := os.WriteFile(outputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, python, "../../tests/official_functions.py", serverURL, token, outputPath, proofPath)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official native functions: %v %s", err, log)
	}
	var proof struct {
		Session string   `json:"session"`
		Turns   []string `json:"turns"`
		Calls   []string `json:"calls"`
	}
	raw, err := os.ReadFile(proofPath)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Turns) != 3 || len(proof.Calls) != 3 {
		t.Fatal(proof, err)
	}
	for i, callID := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, proof.Turns[i], callID)
		if err != nil || call.Applied != (i < 2) {
			t.Fatal(call, err)
		}
	}
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, proof.Session)
	if err != nil || bound.NativeSessionID == "" || bound.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	if requests.Load() != 5 {
		t.Fatal("unexpected replay or missing native continuation", requests.Load())
	}
	t.Logf("Official SDK configured functions, native text/image/error results, application receipts, next Turn and cancellation passed; evidence %s", home)
}

func nativePublicFunctionServer(t *testing.T, h *dispatchHarness, ctx context.Context) (string, string) {
	t.Helper()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	})
	token := uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{TokenSHA256: device.HashCredential(token), TenantID: h.tenant}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, "codex", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL, token
}
