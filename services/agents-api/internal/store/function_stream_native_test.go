package store_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativePublicFunctionStreamHelper(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	h, ctx, home := nativeDispatchHarness(t)
	model, requests := nativeFunctionResultsModel(t, home, []any{
		`{"ticket":"42","status":"open"}`,
		"Tool handler failed.",
	})
	defer model.Close()
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-test-token"}}, nil
	}
	serverURL, token := nativePublicFunctionServer(t, h, ctx)
	proofPath := filepath.Join(home, "public-function-stream.json")
	command := exec.CommandContext(ctx, python, "../../tests/official_function_stream.py", serverURL, token, proofPath)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official native stream helper: %v %s", err, log)
	}
	var proof struct {
		Session string   `json:"session"`
		Turns   []string `json:"turns"`
		Calls   []string `json:"calls"`
	}
	raw, err := os.ReadFile(proofPath)
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Turns) != 2 || len(proof.Calls) != 2 {
		t.Fatal(proof, err)
	}
	for i, callID := range proof.Calls {
		call, err := h.s.GetFunctionCall(ctx, h.tenant, proof.Session, proof.Turns[i], callID)
		if err != nil || !call.Applied {
			t.Fatal(call, err)
		}
	}
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, proof.Session)
	if err != nil || bound.NativeSessionID == "" || bound.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	if requests.Load() != 4 {
		t.Fatal("unexpected replay or missing native continuation", requests.Load())
	}
	t.Logf("Official SDK stream tool handlers, error omission, public history and native application passed; evidence %s", home)
}
