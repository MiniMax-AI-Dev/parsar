package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// EnsureCoreSessionWithModel freezes the model and Provider together before any Core request.
func (s *Store) EnsureCoreSessionWithModel(ctx context.Context, runID string, request json.RawMessage, modelID string) (CoreSessionBinding, error) {
	return s.EnsureCoreSessionWithResources(ctx, runID, request, modelID, nil, "")
}

func (s *Store) EnsureCoreSessionWithResources(ctx context.Context, runID string, request json.RawMessage, modelID string, modelBinding map[string]any, userID string) (CoreSessionBinding, error) {
	if frozen, err := s.GetCoreSession(ctx, runID); err == nil {
		return frozen, nil
	} else if !errors.Is(err, ErrUnknownAgentRun) {
		return CoreSessionBinding{}, err
	}
	if modelID == "" {
		var payload struct {
			Environment map[string]any `json:"environment"`
		}
		if json.Unmarshal(request, &payload) != nil || payload.Environment["skills"] != nil || modelBinding != nil {
			return CoreSessionBinding{}, ErrInvalidInput
		}
		return s.EnsureCoreSession(ctx, runID, request)
	}
	var out CoreSessionBinding
	model, err := uuid(modelID)
	if err != nil {
		return out, ErrCatalogNotFound
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	workspace, err := q.GetCatalogRunWorkspace(ctx, mustUUID(runID))
	if err != nil {
		return out, err
	}
	row, err := q.GetCatalogModelExecution(ctx, sqlc.GetCatalogModelExecutionParams{WorkspaceID: mustUUID(workspace), ID: model})
	if err != nil {
		return out, catalogError(err)
	}
	cipher, err := catalogCipher()
	if err != nil {
		return out, err
	}
	key, err := cipher.Decrypt(row.EncryptedKey)
	if err != nil || key["workspace_id"] != workspace || key["provider_id"] != row.ProviderID {
		return out, ErrCatalogKeyUnavailable
	}
	token, _ := key["api_key"].(string)
	if modelBinding != nil {
		kind, _ := modelBinding["kind"].(string)
		choices, err := credentialbinding.ParseStrict(map[string]any{"credential_bindings": map[string]any{kind: modelBinding}})
		if err != nil {
			return out, ErrInvalidInput
		}
		token, err = s.ResolveAgentCredential(ctx, workspace, userID, kind, choices[kind])
		if err != nil {
			return out, fmt.Errorf("%w: %s", ErrCatalogKeyUnavailable, err)
		}
	}
	provider := v1.ModelProviderInput{Protocol: row.Protocol, BaseURL: row.BaseUrl, APIKey: token, ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens}
	var payload struct {
		Agent       map[string]any `json:"agent"`
		Environment map[string]any `json:"environment"`
	}
	if json.Unmarshal(request, &payload) != nil || payload.Agent == nil {
		return out, ErrInvalidInput
	}
	extension, _ := payload.Agent["x_agents_core"].(map[string]any)
	harness, _ := extension["harness"].(string)
	if err := provider.ValidateHarness(harness); err != nil {
		return out, fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	if payload.Environment["type"] != "openai_hosted" {
		return out, fmt.Errorf("%w: Provider models require a hosted environment", ErrInvalidInput)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(request, &raw); err != nil {
		return out, err
	}
	payload.Agent["model"] = row.ModelKey
	raw["agent"], err = json.Marshal(payload.Agent)
	if err != nil {
		return out, err
	}
	request, err = json.Marshal(raw)
	if err != nil {
		return out, err
	}
	privateEnvironment := map[string]any{}
	if skills, exists := payload.Environment["skills"]; exists {
		privateEnvironment["skills"] = skills
		delete(payload.Environment, "skills")
		raw["environment"], err = json.Marshal(payload.Environment)
		if err != nil {
			return out, err
		}
		request, err = json.Marshal(raw)
		if err != nil {
			return out, err
		}
	}
	bindingID := newID()
	snapshot, err := cipher.Encrypt(map[string]any{"workspace_id": workspace, "binding_id": bindingID, "provider": provider, "environment": privateEnvironment})
	if err != nil {
		return out, ErrCatalogKeyUnavailable
	}
	r, err := q.EnsureCatalogCoreSession(ctx, sqlc.EnsureCatalogCoreSessionParams{ID: mustUUID(bindingID), RunID: mustUUID(runID), Request: request, ProviderSnapshot: snapshot})
	if err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return CoreSessionBinding{ID: pgUUIDString(r.ID), WorkspaceID: pgUUIDString(r.WorkspaceID), SessionID: r.CoreSessionID, Request: r.Request, ProviderSnapshot: r.ProviderSnapshot}, nil
}

// CoreSessionProvider decrypts only when a frozen Session must be created in Core.
func (s *Store) CoreSessionProvider(binding CoreSessionBinding) (*v1.SessionExecutionInput, error) {
	if len(binding.ProviderSnapshot) == 0 {
		return nil, nil
	}
	cipher, err := catalogCipher()
	if err != nil {
		return nil, err
	}
	payload, err := cipher.Decrypt(binding.ProviderSnapshot)
	if err != nil || payload["workspace_id"] != binding.WorkspaceID || payload["binding_id"] != binding.ID {
		return nil, ErrCatalogKeyUnavailable
	}
	raw, err := json.Marshal(payload["provider"])
	if err != nil {
		return nil, ErrCatalogKeyUnavailable
	}
	var provider v1.ModelProviderInput
	if json.Unmarshal(raw, &provider) != nil || provider.Validate() != nil {
		return nil, ErrCatalogKeyUnavailable
	}
	return &v1.SessionExecutionInput{ModelProvider: &provider}, nil
}

// CoreSessionPrivateEnvironment restores initialization only at Core dispatch.
func (s *Store) CoreSessionPrivateEnvironment(binding CoreSessionBinding) (map[string]any, error) {
	if len(binding.ProviderSnapshot) == 0 {
		return nil, nil
	}
	cipher, err := catalogCipher()
	if err != nil {
		return nil, err
	}
	payload, err := cipher.Decrypt(binding.ProviderSnapshot)
	if err != nil || payload["workspace_id"] != binding.WorkspaceID || payload["binding_id"] != binding.ID {
		return nil, ErrCatalogKeyUnavailable
	}
	environment, _ := payload["environment"].(map[string]any)
	return environment, nil
}
