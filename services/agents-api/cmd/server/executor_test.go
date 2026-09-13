package main

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutorRegistryIsExplicitAndRetiresStaticKeys(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", "")
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", "")
	t.Setenv("AGENTS_API_EXECUTOR_URL", "")
	if r, err := executorRegistry(nil, nil); err != nil || r != nil {
		t.Fatal("default registry must remain disabled")
	}
	t.Setenv("AGENTS_API_EXECUTOR_URL", "https://executor.example")
	if _, err := executorRegistry(nil, nil); err == nil {
		t.Fatal("registry enabled without execution owner")
	}
	s, worker := store.New(nil), &execution.Worker{}
	for _, setting := range []string{"AGENTS_API_EXECUTOR_KEYS_FILE", "AGENTS_API_HARNESS_KEYS_FILE"} {
		t.Setenv(setting, "retired-private-file")
		if _, err := executorRegistry(s, worker); err == nil {
			t.Fatal("retired key setting accepted", setting)
		}
		t.Setenv(setting, "")
	}
	registry, err := executorRegistry(s, worker)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
}
