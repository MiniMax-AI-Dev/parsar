package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector/agentsapi"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/coreaccess"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func buildCoreConnector(env func(string) string, st *store.Store) (*agentsapi.Connector, error) {
	var bindings []coreaccess.Binding
	readPrivate := func(name string) ([]byte, error) {
		path, err := paths.MustBeUserPath(name)
		if err != nil {
			return nil, errors.New("Core configuration paths must be absolute")
		}
		return os.ReadFile(path)
	}
	if path := strings.TrimSpace(env("PARSAR_CORE_WORKSPACES_FILE")); path != "" {
		data, err := readPrivate(path)
		if err != nil {
			return nil, errors.New("Core workspace configuration cannot be read")
		}
		if err := json.Unmarshal(data, &bindings); err != nil {
			return nil, errors.New("invalid Core workspace configuration")
		}
	} else if url := strings.TrimSpace(env("PARSAR_CORE_URL")); url != "" {
		bindings = []coreaccess.Binding{{WorkspaceID: strings.TrimSpace(env("PARSAR_CORE_WORKSPACE_ID")), ProjectID: strings.TrimSpace(env("PARSAR_CORE_PROJECT_ID")), BaseURL: url, APIKeyFile: strings.TrimSpace(env("PARSAR_CORE_API_KEY_FILE"))}}
	}
	resolve, err := coreaccess.Build(bindings, readPrivate)
	if err != nil {
		return nil, err
	}
	return agentsapi.NewForWorkspaces(resolve, st), nil
}
