package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Operator options use the existing transient adapter configuration path. They
// are not public Session configuration and are never persisted with its snapshot.
func executionOptions() (func(context.Context, store.Session) (map[string]any, error), error) {
	file := os.Getenv("AGENTS_API_EXECUTION_OPTIONS_FILE")
	if file == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New("cannot read AGENTS_API_EXECUTION_OPTIONS_FILE")
	}
	var check map[string]any
	if json.Unmarshal(raw, &check) != nil || check == nil {
		return nil, errors.New("execution options must contain a JSON object")
	}
	defaultEngine := os.Getenv("AGENTS_API_ENGINE")
	if defaultEngine == "" {
		defaultEngine = "codex"
	}
	var byHarness map[string]json.RawMessage
	if value, exists := check["by_harness"]; exists {
		encoded, _ := json.Marshal(value)
		if len(check) != 1 || json.Unmarshal(encoded, &byHarness) != nil || byHarness == nil {
			return nil, errors.New("execution by_harness options must be an exclusive object")
		}
		for _, entry := range byHarness {
			var object map[string]any
			if json.Unmarshal(entry, &object) != nil || object == nil {
				return nil, errors.New("execution harness options must be objects")
			}
		}
	}
	return func(_ context.Context, session store.Session) (map[string]any, error) {
		selected := raw
		if byHarness != nil {
			var ok bool
			selected, ok = byHarness[session.Engine]
			if !ok {
				return nil, errors.New("execution options are unavailable for the selected harness")
			}
		} else if session.Engine != "" && session.Engine != defaultEngine {
			return nil, errors.New("configure separate execution options for the selected harness")
		}
		// Request assembly may extend the map; no Session may mutate another's options.
		var options map[string]any
		err := json.Unmarshal(selected, &options)
		return options, err
	}, nil
}
