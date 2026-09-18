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
	return func(context.Context, store.Session) (map[string]any, error) {
		// Request assembly may extend the map; no Session may mutate another's options.
		var options map[string]any
		err := json.Unmarshal(raw, &options)
		return options, err
	}, nil
}
