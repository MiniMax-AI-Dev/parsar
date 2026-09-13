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
	harnessFile := os.Getenv("AGENTS_API_HARNESS_KEYS_FILE")
	if file == "" && url == "" && harnessFile == "" {
		return nil, nil
	}
	if file != "" {
		return nil, errors.New("AGENTS_API_EXECUTOR_KEYS_FILE is retired; issue durable credentials with agents-api-environment-key and remove the old setting")
	}
	if url == "" || worker == nil {
		return nil, errors.New("executor registry requires URL and the daemon execution worker")
	}
	var err error
	var harnessKeys []codex.ScopedKey
	if harnessFile != "" {
		harnessKeys, err = readNativeKeys(harnessFile)
		if err != nil {
			return nil, err
		}
	}
	for _, transport := range harnessKeys {
		for _, caller := range callerKeys {
			if strings.EqualFold(transport.TokenSHA256, caller.TokenSHA256) {
				return nil, errors.New("native transport keys must be distinct from caller keys")
			}
		}
	}
	return codex.New(codex.Config{Store: s, CheckOwnership: worker.CheckOwnership, PublicURL: url, HarnessKeys: harnessKeys})
}

func readNativeKeys(file string) ([]codex.ScopedKey, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New("cannot read native transport keys file")
	}
	var keys []codex.ScopedKey
	if err := json.Unmarshal(data, &keys); err != nil || len(keys) == 0 {
		return nil, errors.New("native transport keys must be a nonempty array of scoped digest bindings")
	}
	return keys, nil
}
