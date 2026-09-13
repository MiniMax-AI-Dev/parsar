package main

import (
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func executorRegistry(s *store.Store, worker *execution.Worker) (*codex.Registry, error) {
	file, url := os.Getenv("AGENTS_API_EXECUTOR_KEYS_FILE"), os.Getenv("AGENTS_API_EXECUTOR_URL")
	harnessFile := os.Getenv("AGENTS_API_HARNESS_KEYS_FILE")
	if file == "" && url == "" && harnessFile == "" {
		return nil, nil
	}
	if file != "" {
		return nil, errors.New("AGENTS_API_EXECUTOR_KEYS_FILE is retired; issue durable credentials with agents-api-environment-key and remove the old setting")
	}
	if harnessFile != "" {
		return nil, errors.New("AGENTS_API_HARNESS_KEYS_FILE is retired; harness credentials belong to internal execution ownership; remove the old setting")
	}
	if url == "" || worker == nil {
		return nil, errors.New("executor registry requires URL and the daemon execution worker")
	}
	return codex.New(codex.Config{Store: s, CheckOwnership: worker.CheckOwnership, PublicURL: url})
}
