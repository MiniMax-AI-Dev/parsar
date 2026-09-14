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
	if registry.PublicURL() != "https://executor.example" {
		t.Fatal("public executor origin differs from configured registration origin", registry.PublicURL())
	}
	registry.Close()
}

func TestExecutorPublicOriginUsesRegistryValidation(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTOR_KEYS_FILE", "")
	t.Setenv("AGENTS_API_HARNESS_KEYS_FILE", "")
	for _, origin := range []string{
		"https://token@executor.example", "https://executor.example?token=secret", "https://executor.example?",
		"https://executor.example/#token", "https://executor.example/daemon", "http://executor.example", "",
	} {
		t.Setenv("AGENTS_API_EXECUTOR_URL", origin)
		registry, err := executorRegistry(store.New(nil), func() *execution.Worker { return nil }, func(context.Context) error { return nil })
		if err == nil && registry != nil {
			registry.Close()
			t.Fatal("invalid public executor origin accepted", origin)
		}
	}
	for origin, want := range map[string]string{
		"https://executor.example/": "https://executor.example", "http://127.0.0.1:8091/": "http://127.0.0.1:8091",
	} {
		t.Setenv("AGENTS_API_EXECUTOR_URL", origin)
		registry, err := executorRegistry(store.New(nil), func() *execution.Worker { return nil }, func(context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if registry.PublicURL() != want {
			t.Fatal("incorrect executor registration origin", registry.PublicURL(), want)
		}
		registry.Close()
	}
}
