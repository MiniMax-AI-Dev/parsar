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
	if file == "" || url == "" || worker == nil {
		return nil, errors.New("executor registry requires keys, URL and the daemon execution worker")
	}
	keys, err := readNativeKeys(file)
	if err != nil {
		return nil, err
	}
	var harnessKeys []codex.ScopedKey
	if harnessFile != "" {
		harnessKeys, err = readNativeKeys(harnessFile)
		if err != nil {
			return nil, err
		}
	}
	for _, purpose := range [][]codex.ScopedKey{keys, harnessKeys} {
		for _, transport := range purpose {
			for _, caller := range callerKeys {
				if strings.EqualFold(transport.TokenSHA256, caller.TokenSHA256) {
					return nil, errors.New("native transport keys must be distinct from caller keys")
				}
			}
		}
	}
	return codex.New(codex.Config{Store: s, CheckOwnership: worker.CheckOwnership, PublicURL: url, Keys: keys, HarnessKeys: harnessKeys})
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
