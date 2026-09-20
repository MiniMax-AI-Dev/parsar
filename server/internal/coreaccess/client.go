// Package coreaccess resolves server-owned, workspace-scoped Core credentials.
package coreaccess

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var ErrNotConfigured = errors.New("Core is not configured for this workspace")

type Resolver func(string) (openai.BetaAgentService, error)
type Binding struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	BaseURL     string `json:"base_url"`
	APIKeyFile  string `json:"api_key_file"`
}

// Build rejects accidental credential sharing. Core must bind each credential
// to a distinct project; metadata and caller-controlled headers are not isolation.
func Build(bindings []Binding, readKey func(string) ([]byte, error)) (Resolver, error) {
	clients := map[string]openai.BetaAgentService{}
	credentials := map[string]bool{}
	projects := map[string]bool{}
	for _, binding := range bindings {
		if _, err := uuid.Parse(binding.WorkspaceID); err != nil {
			return nil, errors.New("Core workspace_id must be a UUID")
		}
		if _, exists := clients[binding.WorkspaceID]; exists {
			return nil, errors.New("duplicate Core workspace binding")
		}
		project := strings.TrimSpace(binding.ProjectID)
		scope := strings.TrimRight(binding.BaseURL, "/") + ":" + project
		if project == "" || strings.ContainsAny(project, " \t\r\n") || projects[scope] {
			return nil, errors.New("each workspace requires a distinct Core project_id")
		}
		key, err := readKey(binding.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("Core key file cannot be read for workspace %s", binding.WorkspaceID)
		}
		value := strings.TrimSpace(string(key))
		if credentials[value] {
			return nil, errors.New("Core caller credentials must not be shared across workspaces")
		}
		client, err := agentsclient.NewAgents(agentsclient.Config{BaseURL: binding.BaseURL, APIKey: value, HTTPClient: &http.Client{Timeout: 6 * time.Minute}})
		if err != nil {
			return nil, err
		}
		client = openai.NewBetaAgentService(append(client.Options, option.WithHeader("OpenAI-Project", project))...)
		clients[binding.WorkspaceID] = client
		projects[scope] = true
		credentials[value] = true
	}
	return func(workspaceID string) (openai.BetaAgentService, error) {
		client, ok := clients[workspaceID]
		if !ok {
			return openai.BetaAgentService{}, ErrNotConfigured
		}
		return client, nil
	}, nil
}
