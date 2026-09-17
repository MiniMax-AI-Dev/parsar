package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentPlacement struct {
	Type                  string   `json:"type"`
	WorkspaceDirectory    string   `json:"workspace_directory"`
	CapabilityDirectories []string `json:"capability_directories"`
}

func parseEnvironmentPlacement(configuration json.RawMessage) (environmentPlacement, error) {
	var placement environmentPlacement
	if json.Unmarshal(configuration, &placement) != nil {
		return placement, store.ErrInvalidInput
	}
	switch placement.Type {
	case "self_hosted":
		if placement.WorkspaceDirectory != "" && len(placement.CapabilityDirectories) == 0 {
			return placement, nil
		}
	case "openai_hosted":
		// This private profile qualifies only explicit network denial, not the enabled default.
		var local struct {
			Type    string `json:"type"`
			Network struct {
				Access string `json:"access"`
			} `json:"network"`
		}
		decoder := json.NewDecoder(bytes.NewReader(configuration))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&local) == nil && local.Network.Access == "disabled" {
			return placement, nil
		}
	}
	return placement, store.ErrInvalidInput
}

func environmentDeviceMatches(session store.Session, environment store.Environment, bound store.ExecutionDevice, placement environmentPlacement) bool {
	if environment.SessionID != session.ID || environment.TenantID != session.TenantID {
		return false
	}
	if placement.Type == "openai_hosted" {
		return bound.EnvironmentID == environment.ID
	}
	return bound.EnvironmentID == ""
}

func (d *Dispatcher) configurePreparedEnvironment(ctx context.Context, session store.Session, environment store.Environment, bound store.ExecutionDevice, req *proto.PromptRequestPayload) (func(), error) {
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil || !environmentDeviceMatches(session, environment, bound, placement) {
		return nil, store.ErrInvalidInput
	}
	if placement.Type == "openai_hosted" {
		req.LocalEnvironment = &proto.LocalEnvironment{ID: environment.ID}
		return nil, nil
	}
	if d.EnvironmentConnection == nil {
		return nil, errors.New("environment connection resolver is not configured")
	}
	connection, err := d.EnvironmentConnection(ctx, session, environment)
	if err != nil {
		return connection.Release, err
	}
	if connection.URL == "" || connection.Token == "" || connection.Release == nil {
		return connection.Release, errors.New("environment connection is incomplete")
	}
	req.RemoteEnvironment = &proto.RemoteEnvironment{ID: environment.ID, WorkspaceDirectory: placement.WorkspaceDirectory, ConnectionURL: connection.URL, ConnectionToken: connection.Token}
	return connection.Release, nil
}
