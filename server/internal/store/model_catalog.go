package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/jackc/pgx/v5"
)

var ErrCatalogNotFound = errors.New("model or provider is unavailable in this workspace")
var ErrCatalogKeyUnavailable = errors.New("model provider encryption is unavailable")

type CatalogProvider struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	BaseURL       string `json:"base_url"`
	KeyConfigured bool   `json:"key_configured"`
}
type CatalogModel struct {
	ContextWindow   int32  `json:"context_window"`
	MaxOutputTokens int32  `json:"max_output_tokens"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	ModelKey        string `json:"model_key"`
	ProviderID      string `json:"provider_id"`
	ProviderName    string `json:"provider_name,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
}
type CatalogProviderInput struct {
	Name     string  `json:"name"`
	Protocol string  `json:"protocol" enums:"anthropic,responses"`
	BaseURL  string  `json:"base_url"`
	APIKey   *string `json:"api_key,omitempty"`
}
type CatalogModelInput struct {
	Name            string `json:"name"`
	ModelKey        string `json:"model_key"`
	ProviderID      string `json:"provider_id"`
	ContextWindow   int32  `json:"context_window"`
	MaxOutputTokens int32  `json:"max_output_tokens"`
}

func catalogName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return "", fmt.Errorf("%w: name and model identifier must be 1–256 bytes", ErrInvalidInput)
	}
	return value, nil
}
func catalogCipher() (*secrets.Service, error) {
	cipher, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
	if err != nil {
		return nil, ErrCatalogKeyUnavailable
	}
	return cipher, nil
}
func catalogError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCatalogNotFound
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: this model identifier already exists under the Provider", ErrInvalidInput)
	}
	return err
}

func (s *Store) ListCatalogProviders(ctx context.Context, workspaceID string) ([]CatalogProvider, error) {
	id, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListCatalogProviders(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogProvider, 0, len(rows))
	for _, r := range rows {
		out = append(out, CatalogProvider{r.ID, r.Name, r.Protocol, r.BaseUrl, true})
	}
	return out, nil
}
func (s *Store) SaveCatalogProvider(ctx context.Context, workspaceID, providerID string, input CatalogProviderInput) (CatalogProvider, error) {
	var out CatalogProvider
	name, err := catalogName(input.Name)
	if err != nil {
		return out, err
	}
	workspace, err := uuid(workspaceID)
	if err != nil {
		return out, err
	}
	key := "unchanged"
	if input.APIKey != nil {
		key = *input.APIKey
	}
	if providerID == "" && input.APIKey == nil {
		return out, fmt.Errorf("%w: API key is required", ErrInvalidInput)
	}
	provider := v1.ModelProviderInput{Protocol: input.Protocol, BaseURL: strings.TrimSpace(input.BaseURL), APIKey: key}
	if err := provider.Validate(); err != nil {
		return out, fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	creating := providerID == ""
	if creating {
		providerID = newID()
	}
	providerUUID, err := uuid(providerID)
	if err != nil {
		return out, fmt.Errorf("%w: invalid provider ID", ErrInvalidInput)
	}
	providerID = pgUUIDString(providerUUID)
	workspaceID = pgUUIDString(workspace)
	var encrypted []byte
	if input.APIKey != nil {
		cipher, err := catalogCipher()
		if err != nil {
			return out, err
		}
		encrypted, err = cipher.Encrypt(map[string]any{"workspace_id": workspaceID, "provider_id": providerID, "api_key": key})
		if err != nil {
			return out, ErrCatalogKeyUnavailable
		}
	}
	q := sqlc.New(s.db)
	if creating {
		r, err := q.CreateCatalogProvider(ctx, sqlc.CreateCatalogProviderParams{ID: mustUUID(providerID), WorkspaceID: workspace, Name: name, Protocol: provider.Protocol, BaseUrl: provider.BaseURL, EncryptedKey: encrypted})
		if err != nil {
			return out, catalogError(err)
		}
		out = CatalogProvider{r.ID, r.Name, r.Protocol, r.BaseUrl, true}
	} else {
		id, err := uuid(providerID)
		if err != nil {
			return out, err
		}
		r, err := q.UpdateCatalogProvider(ctx, sqlc.UpdateCatalogProviderParams{ID: id, WorkspaceID: workspace, Name: name, Protocol: provider.Protocol, BaseUrl: provider.BaseURL, EncryptedKey: encrypted})
		if err != nil {
			return out, catalogError(err)
		}
		out = CatalogProvider{r.ID, r.Name, r.Protocol, r.BaseUrl, true}
	}
	return out, nil
}
func (s *Store) DeleteCatalogProvider(ctx context.Context, workspaceID, providerID string) error {
	workspace, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	id, err := uuid(providerID)
	if err != nil {
		return err
	}
	count, err := sqlc.New(s.db).DeleteCatalogProvider(ctx, sqlc.DeleteCatalogProviderParams{WorkspaceID: workspace, ID: id})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrCatalogNotFound
	}
	return nil
}
func (s *Store) ListCatalogModels(ctx context.Context, workspaceID string) ([]CatalogModel, error) {
	workspace, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListCatalogModels(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(rows))
	for _, r := range rows {
		out = append(out, CatalogModel{ID: r.ID, Name: r.Name, ModelKey: r.ModelKey, ProviderID: r.ProviderID, ProviderName: r.ProviderName, Protocol: r.Protocol, ContextWindow: r.ContextWindow, MaxOutputTokens: r.MaxOutputTokens})
	}
	return out, nil
}
func (s *Store) CreateCatalogModel(ctx context.Context, workspaceID, actorID string, input CatalogModelInput) (CatalogModel, error) {
	var out CatalogModel
	name, err := catalogName(input.Name)
	if err != nil {
		return out, err
	}
	key, err := catalogName(input.ModelKey)
	if err != nil {
		return out, err
	}
	if input.ContextWindow < 0 || input.MaxOutputTokens < 0 || input.MaxOutputTokens > input.ContextWindow {
		return out, fmt.Errorf("%w: invalid model token limits", ErrInvalidInput)
	}
	workspace, err := uuid(workspaceID)
	if err != nil {
		return out, err
	}
	provider, err := uuid(input.ProviderID)
	if err != nil {
		return out, fmt.Errorf("%w: invalid provider ID", ErrInvalidInput)
	}
	row, err := sqlc.New(s.db).CreateCatalogModel(ctx, sqlc.CreateCatalogModelParams{ID: mustUUID(newID()), Slug: generateAutoSlug("model"), Name: name, ModelKey: key, ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens, WorkspaceID: workspace, ProviderID: provider, CreatedBy: nullableUUID(actorID)})
	if err != nil {
		return out, catalogError(err)
	}
	return CatalogModel{ID: row.ID, Name: row.Name, ModelKey: row.ModelKey, ProviderID: row.ProviderID, ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens}, nil
}
func (s *Store) RenameCatalogModel(ctx context.Context, workspaceID, modelID, name string) (CatalogModel, error) {
	var out CatalogModel
	name, err := catalogName(name)
	if err != nil {
		return out, err
	}
	workspace, err := uuid(workspaceID)
	if err != nil {
		return out, err
	}
	id, err := uuid(modelID)
	if err != nil {
		return out, err
	}
	row, err := sqlc.New(s.db).RenameCatalogModel(ctx, sqlc.RenameCatalogModelParams{WorkspaceID: workspace, ID: id, Name: name})
	if err != nil {
		return out, catalogError(err)
	}
	return CatalogModel{ID: row.ID, Name: row.Name, ModelKey: row.ModelKey, ProviderID: row.ProviderID, ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens}, nil
}
func (s *Store) DeleteCatalogModel(ctx context.Context, workspaceID, modelID string) error {
	workspace, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	id, err := uuid(modelID)
	if err != nil {
		return err
	}
	count, err := sqlc.New(s.db).DeleteCatalogModel(ctx, sqlc.DeleteCatalogModelParams{WorkspaceID: workspace, ID: id})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrCatalogNotFound
	}
	return nil
}

func (s *Store) ResolveCatalogAgentModel(ctx context.Context, workspaceID string, config map[string]any) error {
	value, supplied := config["model_id"]
	if !supplied {
		return nil
	}
	modelID, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: model_id must be a catalog UUID", ErrInvalidInput)
	}
	workspace, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	id, err := uuid(modelID)
	if err != nil {
		return fmt.Errorf("%w: model_id must be a catalog UUID", ErrInvalidInput)
	}
	row, err := sqlc.New(s.db).GetCatalogModelChoice(ctx, sqlc.GetCatalogModelChoiceParams{WorkspaceID: workspace, ID: id})
	if err != nil {
		return catalogError(err)
	}
	var harness string
	if extension, ok := config["x_agents_core"].(map[string]any); ok {
		harness, _ = extension["harness"].(string)
	}
	if err := v1.ValidateModelProtocol(row.Protocol, harness); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	if harness == "mcode" && (row.ContextWindow == 0 || row.MaxOutputTokens == 0) {
		return fmt.Errorf("%w: MiniMax Code requires model token limits", ErrInvalidInput)
	}
	env, err := ParseCoreEnvironment(config["environment"])
	if err != nil {
		return err
	}
	if env.Type != "openai_hosted" {
		return fmt.Errorf("%w: Provider models currently require a hosted environment", ErrInvalidInput)
	}
	config["model"] = row.ModelKey
	return nil
}

// RecordCatalogAudit deliberately excludes endpoints and confidential mutation input.
func (s *Store) RecordCatalogAudit(workspace, actor, kind, id, action string) {
	s.emitAgentAudit(time.Now().UTC(), actor, "model_catalog."+kind+"."+action, "model_"+kind, id, workspace, nil)
}
