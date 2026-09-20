package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) sessionModelOptions(ctx context.Context, session store.Session, model string) (map[string]any, error) {
	if d.Store == nil {
		return nil, errors.New("session model configuration is unavailable")
	}
	provider, err := d.Store.SessionModelExecution(ctx, session.TenantID, session.ID)
	if err != nil {
		return nil, err
	}
	if err := provider.ValidateHarness(session.Engine); err != nil {
		return nil, err
	}
	switch session.Engine {
	case "codex":
		return map[string]any{"codex_provider": map[string]any{"base_url": provider.BaseURL, "bearer_token": provider.APIKey, "wire_api": "responses"}}, nil
	case "claude_sdk":
		return map[string]any{"claude_provider": map[string]any{"base_url": provider.BaseURL, "bearer_token": provider.APIKey}}, nil
	case "mcode":
		return map[string]any{"mcode_provider": map[string]any{"name": "Configured provider", "kind": "custom", "enabled": true, "npm": "@ai-sdk/anthropic", "options": map[string]any{"baseURL": provider.BaseURL, "apiKey": provider.APIKey}, "models": map[string]any{model: map[string]any{"name": model, "tool_call": true, "limit": map[string]any{"context": provider.ContextWindow, "output": provider.MaxOutputTokens}}}}}, nil
	default:
		return nil, errors.New("unsupported model provider harness")
	}
}
