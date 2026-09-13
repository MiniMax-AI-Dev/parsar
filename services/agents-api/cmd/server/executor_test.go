package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestExecutorRegistryIsExplicitAndUsesDistinctKeys(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", "")
	t.Setenv("AGENTS_API_EXECUTOR_URL", "")
	if r, err := executorRegistry(nil, nil, nil); err != nil || r != nil {
		t.Fatal("default registry must remain disabled")
	}
	t.Setenv("AGENTS_API_EXECUTOR_URL", "https://executor.example")
	if _, err := executorRegistry(nil, nil, nil); err == nil {
		t.Fatal("partial configuration accepted")
	}
	key := codex.ExecutorKey{TokenSHA256: device.HashCredential(uuid.NewString()), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	encoded, _ := json.Marshal([]codex.ExecutorKey{key})
	file := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(file, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", file)
	s := store.New(nil)
	if _, err := executorRegistry(s, nil, nil); err == nil {
		t.Fatal("registry enabled without execution owner")
	}
	worker := &execution.Worker{}
	if _, err := executorRegistry(s, worker, []api.APIKey{{TokenSHA256: key.TokenSHA256, TenantID: key.TenantID}}); err == nil {
		t.Fatal("caller key also granted executor access")
	}
	r, err := executorRegistry(s, worker, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}
