package api

import (
	"encoding/json"
	"path"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func decodeSessionEnvironment(raw json.RawMessage) (*v1.Environment, error) {
	var environment v1.Environment
	if json.Unmarshal(raw, &environment) != nil {
		return nil, store.ErrInvalidInput
	}
	fields := []string{"type"}
	switch environment.Type {
	case "none":
	case "self_hosted":
		fields = append(fields, "workspace_directory", "capability_directories")
		if !path.IsAbs(environment.WorkspaceDirectory) || strings.ContainsRune(environment.WorkspaceDirectory, 0) {
			return nil, store.ErrInvalidInput
		}
	default:
		return nil, store.ErrInvalidInput
	}
	if err := decodeInputObject(raw, &environment, fields...); err != nil {
		return nil, err
	}
	return &environment, nil
}
