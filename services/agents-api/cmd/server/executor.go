package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func executorRegistry(s *store.Store, worker *execution.Worker, callerKeys []api.APIKey) (*codex.Registry, error) {
	file, url := os.Getenv("AGENTS_API_EXECUTOR_KEYS_FILE"), os.Getenv("AGENTS_API_EXECUTOR_URL")
	if file == "" && url == "" {
		return nil, nil
	}
	if file == "" || url == "" || worker == nil {
		return nil, errors.New("executor registry requires keys, URL and the daemon execution worker")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New("cannot read AGENTS_API_EXECUTOR_KEYS_FILE")
	}
	var keys []codex.ExecutorKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, errors.New("executor keys must be an array of scoped digest bindings")
	}
	for _, executor := range keys {
		for _, caller := range callerKeys {
			if strings.EqualFold(executor.TokenSHA256, caller.TokenSHA256) {
				return nil, errors.New("executor keys must be distinct from caller keys")
			}
		}
	}
	return codex.New(codex.Config{Store: s, CheckOwnership: worker.CheckOwnership, PublicURL: url, Keys: keys})
}
