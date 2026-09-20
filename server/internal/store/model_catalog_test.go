package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestModelCatalogOwnershipAndFrozenCredentials(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "catalog-test-encryption-key")
	db := openTestDB(t)
	st := New(db)
	ctx := t.Context()
	ids := mustSeedDevFixture(t, ctx, st)
	key := "catalog-private-key-canary"
	provider, err := st.SaveCatalogProvider(ctx, ids.WorkspaceID, "", CatalogProviderInput{Name: "Provider", Protocol: "anthropic", BaseURL: "https://example.com/anthropic", APIKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	model, err := st.CreateCatalogModel(ctx, ids.WorkspaceID, ids.UserID, CatalogModelInput{Name: "Model", ModelKey: "actual-model", ProviderID: provider.ID, ContextWindow: 100000, MaxOutputTokens: 8000})
	if err != nil {
		t.Fatal(err)
	}
	models, err := st.ListCatalogModels(ctx, ids.WorkspaceID)
	if err != nil || len(models) != 1 || models[0].ModelKey != "actual-model" {
		t.Fatal("catalog includes legacy rows", err)
	}
	other := newID()
	if rows, err := st.ListCatalogModels(ctx, other); err != nil || len(rows) != 0 {
		t.Fatal("foreign models", err)
	}
	if _, err := st.RenameCatalogModel(ctx, other, model.ID, "foreign"); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatal("foreign edit", err)
	}
	if _, err := st.SaveCatalogProvider(ctx, other, provider.ID, CatalogProviderInput{Name: "foreign", Protocol: "anthropic", BaseURL: "https://example.com"}); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatal("foreign Provider edit", err)
	}
	if _, err := st.CreateCatalogModel(ctx, ids.WorkspaceID, ids.UserID, CatalogModelInput{Name: "duplicate", ModelKey: "actual-model", ProviderID: provider.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("duplicate identifier accepted", err)
	}
	config := map[string]any{"model_id": model.ID, "model": "wrong", "x_agents_core": map[string]any{"harness": "mcode"}, "environment": map[string]any{"type": "openai_hosted"}}
	if err := st.ResolveCatalogAgentModel(ctx, ids.WorkspaceID, config); err != nil || config["model"] != "actual-model" {
		t.Fatal("catalog model mapping", err)
	}
	config["x_agents_core"] = map[string]any{"harness": "codex"}
	if err := st.ResolveCatalogAgentModel(ctx, ids.WorkspaceID, config); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unsupported protocol accepted", err)
	}
	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, PrimaryAgentID: ids.BackendAgentID, Title: "catalog snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	send, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: conv.ID, UserID: ids.UserID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	request := json.RawMessage(`{"agent":{"model":"stale","x_agents_core":{"harness":"mcode"}},"environment":{"type":"openai_hosted"}}`)
	binding, err := st.EnsureCoreSessionWithModel(ctx, send.RunIDs[0], request, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binding.Request, []byte("actual-model")) || bytes.Contains(binding.Request, []byte(key)) || bytes.Contains(binding.ProviderSnapshot, []byte(key)) {
		t.Fatal("invalid frozen configuration")
	}
	var encrypted []byte
	if err := db.QueryRow(ctx, "SELECT encrypted_key FROM model_providers WHERE id=$1", provider.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte(key)) {
		t.Fatal("plaintext Provider key", err)
	}
	public, _ := st.ListCatalogProviders(ctx, ids.WorkspaceID)
	raw, _ := json.Marshal(public)
	if bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte("encrypted_key")) {
		t.Fatal("public credential disclosure")
	}
	rotated := "rotated-key"
	if _, err := st.SaveCatalogProvider(ctx, ids.WorkspaceID, provider.ID, CatalogProviderInput{Name: "Renamed", Protocol: "anthropic", BaseURL: "https://new.example.com", APIKey: &rotated}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteCatalogProvider(ctx, ids.WorkspaceID, provider.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(db)
	repeated, err := restarted.EnsureCoreSessionWithModel(ctx, send.RunIDs[0], request, newID())
	if err != nil || repeated.ID != binding.ID {
		t.Fatal("retry consulted mutable catalog", err)
	}
	execution, err := restarted.CoreSessionProvider(repeated)
	if err != nil || execution.ModelProvider.APIKey != key || execution.ModelProvider.BaseURL != "https://example.com/anthropic" {
		t.Fatal("credential snapshot changed", err)
	}
	repeated.ID = newID()
	if _, err := st.CoreSessionProvider(repeated); !errors.Is(err, ErrCatalogKeyUnavailable) {
		t.Fatal("snapshot binding not authenticated", err)
	}
	if err := st.ResolveCatalogAgentModel(ctx, ids.WorkspaceID, config); !errors.Is(err, ErrCatalogNotFound) {
		t.Fatal("deleted model still selectable", err)
	}
}
