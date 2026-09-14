package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// WithEnvironmentRemoteURL enables self-hosted output using the composition's validated executor origin.
func WithEnvironmentRemoteURL(origin string) Option {
	return func(h *Handler) { h.executorURL = origin }
}

func sessionResponse(session store.Session, executorURL string) (v1.Session, error) {
	var cfg configuration
	if err := json.Unmarshal(session.Configuration, &cfg); err != nil || cfg.Agent.ID == "" || cfg.Agent.Model == "" {
		return v1.Session{}, errors.New("unsupported stored session configuration")
	}
	environment, err := sessionEnvironment(session, cfg.Environment.Type, executorURL)
	if err != nil {
		return v1.Session{}, err
	}
	response := v1.Session{
		ID: session.ID, Agent: cfg.Agent, Environment: environment, Usage: tokenUsage(session.Usage),
		CreatedAt: session.CreatedAt.Unix(), LastActiveAt: session.CreatedAt.Unix(),
		Metadata: session.Metadata, Object: "agent.session", Status: "idle",
		RequiredActions: []v1.RequiredAction{}, VaultIDs: []string{},
	}
	if turn := session.LastTurn; turn != nil {
		active := turn.CreatedAt
		if turn.StartedAt.After(active) {
			active = turn.StartedAt
		}
		if turn.CompletedAt.After(active) {
			active = turn.CompletedAt
		}
		response.LastActiveAt = active.Unix()
		switch turn.Status {
		case store.TurnQueued, store.TurnInProgress, store.TurnWaiting:
			response.Status = "in_progress"
			if turn.CancelRequestedAt.IsZero() && len(session.RequiredActions) > 0 {
				response.Status = "requires_action"
				for _, action := range session.RequiredActions {
					response.RequiredActions = append(response.RequiredActions, v1.RequiredAction{
						Type: action.Type, Arguments: action.Arguments, CallID: action.CallID, Name: action.Name, TurnID: action.TurnID,
					})
				}
			}
		case store.TurnFailed:
			response.Status = "failed"
			message := "The execution could not complete."
			response.Error = &message
		}
	}
	if activity := session.EnvironmentInputActivity; activity != nil {
		if activity.Status != "idle" && activity.Status != "requires_action" {
			return v1.Session{}, errors.New("unsupported stored environment input activity")
		}
		response.Status, response.Error = activity.Status, nil
		response.LastActiveAt = activity.LastActiveAt.Unix()
		response.RequiredActions = []v1.RequiredAction{}
		if activity.Status == "requires_action" {
			if activity.EnvironmentID == "" || activity.EnvironmentID != environment.ID {
				return v1.Session{}, errors.New("invalid stored environment connection action")
			}
			response.RequiredActions = append(response.RequiredActions, v1.RequiredAction{Type: "environment_connection", EnvironmentID: activity.EnvironmentID})
		}
	}
	return response, nil
}

func sessionEnvironment(session store.Session, kind, executorURL string) (v1.SessionEnvironment, error) {
	if kind == "none" {
		return v1.SessionEnvironment{Type: "none"}, nil
	}
	environment := session.Environment
	if kind != "self_hosted" || environment == nil || environment.ID == "" || environment.SessionID != session.ID || environment.TenantID != session.TenantID || executorURL == "" {
		return v1.SessionEnvironment{}, errors.New("unsupported stored session environment")
	}
	var cfg struct {
		Type                  string   `json:"type"`
		CapabilityDirectories []string `json:"capability_directories"`
		WorkspaceDirectory    string   `json:"workspace_directory"`
	}
	if err := json.Unmarshal(environment.Configuration, &cfg); err != nil || cfg.Type != kind || cfg.WorkspaceDirectory == "" {
		return v1.SessionEnvironment{}, errors.New("unsupported stored session environment configuration")
	}
	if cfg.CapabilityDirectories == nil {
		cfg.CapabilityDirectories = []string{}
	}
	return v1.SessionEnvironment{
		Type: kind, ID: environment.ID, CapabilityDirectories: &cfg.CapabilityDirectories,
		RemoteURL: executorURL, WorkspaceDirectory: cfg.WorkspaceDirectory,
	}, nil
}
