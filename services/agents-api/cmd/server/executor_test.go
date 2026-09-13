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
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", "")
	t.Setenv("AGENTS_API_EXECUTOR_URL", "")
	if r, err := executorRegistry(nil, nil, nil); err != nil || r != nil {
		t.Fatal("default registry must remain disabled")
	}
	t.Setenv("AGENTS_API_EXECUTOR_URL", "https://executor.example")
	if _, err := executorRegistry(nil, nil, nil); err == nil {
		t.Fatal("partial configuration accepted")
	}
	key := codex.ScopedKey{TokenSHA256: device.HashCredential(uuid.NewString()), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	encoded, _ := json.Marshal([]codex.ScopedKey{key})
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

func TestHarnessRegistryConfigurationSeparatesAllPurposes(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", "")
	t.Setenv("AGENTS_API_EXECUTOR_URL", "")
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", "configured")
	if _, err := executorRegistry(nil, nil, nil); err == nil {
		t.Fatal("harness enabled without registry")
	}
	directory := t.TempDir()
	key := codex.ScopedKey{TokenSHA256: device.HashCredential(uuid.NewString()), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	write := func(name string, value codex.ScopedKey) string {
		t.Helper()
		data, err := json.Marshal([]codex.ScopedKey{value})
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(directory, name)
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", write("executor.json", key))
	t.Setenv("AGENTS_API_EXECUTOR_URL", "https://executor.example")
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", write("harness.json", key))
	s, worker := store.New(nil), &execution.Worker{}
	if _, err := executorRegistry(s, worker, nil); err == nil {
		t.Fatal("harness reuses executor credential")
	}
	key.TokenSHA256 = device.HashCredential(uuid.NewString())
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", write("harness.json", key))
	if _, err := executorRegistry(s, worker, []api.APIKey{{TokenSHA256: key.TokenSHA256, TenantID: key.TenantID}}); err == nil {
		t.Fatal("harness reuses caller credential")
	}
	registry, err := executorRegistry(s, worker, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
}
