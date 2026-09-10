// Package httpagent connects self-managed HTTP services to the standard run dispatcher.
package httpagent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Store interface {
	GetHTTPAgentSecretPayload(context.Context, string, string) (store.SecretPayload, error)
	GetAgentRun(context.Context, string) (store.AgentRunDetailRead, error)
}

type activeRun struct {
	conversationID string
	cancel         context.CancelFunc
}
type Connector struct {
	store  Store
	vault  *secrets.Service
	client *http.Client
	mu     sync.Mutex
	runs   map[string]*activeRun
}

func New(runtimeStore Store, masterKey string, client *http.Client) *Connector {
	vault, _ := secrets.New(masterKey)
	return &Connector{store: runtimeStore, vault: vault, client: client, runs: make(map[string]*activeRun)}
}
func (*Connector) Type() string { return "http" }
func (*Connector) Capabilities() connector.Capabilities {
	return connector.Capabilities{Sync: true, Streaming: true, Cancellation: true, Usage: true}
}

// Register cancellation before dispatch, then read durable status. A cancellation
// that won before registration must not start a new outbound request.
func (c *Connector) begin(ctx context.Context, in connector.PromptInput) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	run := &activeRun{conversationID: in.ConversationID, cancel: cancel}
	c.mu.Lock()
	if _, exists := c.runs[in.RunID]; exists {
		c.mu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("HTTP Agent run is already in progress")
	}
	c.runs[in.RunID] = run
	c.mu.Unlock()
	cleanup := func() {
		cancel()
		c.mu.Lock()
		if c.runs[in.RunID] == run {
			delete(c.runs, in.RunID)
		}
		c.mu.Unlock()
	}
	state, err := c.store.GetAgentRun(ctx, in.RunID)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("could not read HTTP Agent run status")
	}
	if state.Status != "running" && state.Status != "queued" {
		cleanup()
		return nil, nil, context.Canceled
	}
	return ctx, cleanup, nil
}

func (c *Connector) Prompt(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	ctx, cleanup, err := c.begin(ctx, in)
	if err != nil {
		return connector.PromptOutput{}, err
	}
	defer cleanup()
	return c.invoke(ctx, in)
}
func (c *Connector) StreamPrompt(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	ctx, cleanup, err := c.begin(ctx, in)
	if err != nil {
		return nil, err
	}
	events := make(chan connector.PromptEvent, 2)
	go func() {
		defer cleanup()
		defer close(events)
		result, err := c.invoke(ctx, in)
		if err != nil {
			events <- connector.PromptEvent{Type: connector.EventError, Error: err.Error()}
			events <- connector.PromptEvent{Type: connector.EventDone, Sequence: 1}
			return
		}
		events <- connector.PromptEvent{Type: connector.EventDone, Sequence: 1, Final: &result}
	}()
	return events, nil
}

func (c *Connector) invoke(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	config := store.HTTPAgentConfigFrom(in.AgentConfig)
	headers := map[string]string{}
	if config.SecretID != "" {
		secret, err := c.store.GetHTTPAgentSecretPayload(ctx, in.WorkspaceID, config.SecretID)
		if err != nil || c.vault == nil {
			return connector.PromptOutput{}, fmt.Errorf("HTTP Agent authentication is unavailable; check its workspace credential")
		}
		payload, err := c.vault.Decrypt(secret.EncryptedPayload)
		if err != nil {
			return connector.PromptOutput{}, fmt.Errorf("HTTP Agent credential could not be decrypted")
		}
		token, _ := payload["token"].(string)
		token = strings.TrimSpace(token)
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return connector.PromptOutput{}, fmt.Errorf("HTTP Agent bearer credential is invalid")
		}
		headers["Authorization"] = "Bearer " + token
	}
	// Only behavior instructions cross the boundary. Model/capability credential
	// references and runtime configuration belong to Parsar and are never forwarded.
	publicConfig := map[string]any{}
	if prompt, ok := in.AgentConfig["system_prompt"].(string); ok {
		publicConfig["system_prompt"] = prompt
	}
	result, err := httprunner.Send(ctx, c.client, config.Endpoint, headers, httprunner.AgentRequest{
		RunID: in.RunID, WorkspaceID: in.WorkspaceID, ConversationID: in.ConversationID,
		AgentID: in.AgentID, AgentName: in.AgentName, AgentSlug: in.AgentSlug,
		TriggerMessageContent: in.TriggerMessageContent, AgentConfig: publicConfig,
	})
	if err != nil {
		return connector.PromptOutput{}, err
	}
	return connector.PromptOutput{Content: result.Content, Usage: result.Usage}, nil
}

func (c *Connector) Abort(_ context.Context, in connector.AbortInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if run := c.runs[in.RunID]; run != nil && run.conversationID == in.ConversationID {
		run.cancel()
	}
	return nil
}
func (c *Connector) Cancel(_ context.Context, conversationID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, run := range c.runs {
		if run.conversationID == conversationID {
			run.cancel()
		}
	}
	return nil
}
func (c *Connector) Close(ctx context.Context, conversationID string) error {
	return c.Cancel(ctx, conversationID)
}
func (*Connector) SubmitPermission(context.Context, connector.PermissionDecision) error {
	return connector.ErrNotSupported
}
func (*Connector) SubmitPromptForUserChoice(context.Context, connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}

var _ connector.AgentConnector = (*Connector)(nil)
