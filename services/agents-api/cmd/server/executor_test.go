package main

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutorRegistryIsExplicitAndRetiresStaticKeys(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", "")
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", "")
	t.Setenv("AGENTS_API_EXECUTOR_URL", "")
	if r, err := executorRegistry(nil, nil, nil); err != nil || r != nil {
		t.Fatal("default registry must remain disabled")
	}
	t.Setenv("AGENTS_API_EXECUTOR_URL", "https://executor.example")
	if _, err := executorRegistry(nil, nil, nil); err == nil {
		t.Fatal("registry enabled without execution owner")
	}
	s := store.New(nil)
	checkOwnership := func(context.Context) error {
		t.Fatal("constructor invoked ownership before the worker was initialized")
		return nil
	}
	for _, setting := range []string{"AGENTS_API_EXECUTOR_KEYS_FILE", "AGENTS_API_HARNESS_KEYS_FILE"} {
		t.Setenv(setting, "retired-private-file")
		if _, err := executorRegistry(s, func() *execution.Worker { return nil }, checkOwnership); err == nil {
			t.Fatal("retired key setting accepted", setting)
		}
		t.Setenv(setting, "")
	}
	registry, err := executorRegistry(s, func() *execution.Worker { return nil }, checkOwnership)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
}
