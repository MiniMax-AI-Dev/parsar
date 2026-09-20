package store

import (
	"bytes"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
	"os"
	"strings"
	"testing"
)

func TestAgentResourceWritesAreAtomicAndPreserveBindings(t *testing.T) {
	st := New(openTestDB(t))
	ctx := t.Context()
	ids := mustSeedDevFixture(t, ctx, st)
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{WorkspaceID: ids.WorkspaceID, Type: "system_prompt", Name: "instructions", CreatorID: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	version, err := st.CreateCapabilityVersion(ctx, CreateCapabilityVersionInput{CapabilityID: capability.ID, Version: "1", CreatorID: ids.UserID, CanonicalSpec: json.RawMessage(`{"schema_version":1,"kind":"system_prompt","system_prompt":{"prompt":"Use the saved resource."}}`)})
	if err != nil {
		t.Fatal(err)
	}
	binding := InitialAgentCapabilityInput{CapabilityVersionID: version.ID, PinningMode: "pinned", Configuration: map[string]any{"setting": "original"}}
	created, err := st.CreateAgent(ctx, CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "Resources", ConnectorType: "agents_api", CreatedBy: ids.UserID, AgentConfig: map[string]any{"model": "fixture"}, InitialCapabilities: []InitialAgentCapabilityInput{binding}})
	if err != nil {
		t.Fatal(err)
	}
	originalID := created.InitialCapabilities[0].ID
	name := "Should roll back"
	_, _, err = st.UpdateAgent(ctx, UpdateAgentInput{AgentID: created.Agent.ID, ActorID: ids.UserID, Name: &name, ResourceBindingsSet: true, ResourceBindings: []InitialAgentCapabilityInput{{CapabilityVersionID: newID()}}})
	if err == nil {
		t.Fatal("unknown resource accepted")
	}
	agent, err := st.GetAgent(ctx, created.Agent.ID)
	if err != nil || agent.Name != "Resources" {
		t.Fatal("partial Agent write", err)
	}
	binding.Configuration = map[string]any{"setting": "changed"}
	_, _, err = st.UpdateAgent(ctx, UpdateAgentInput{AgentID: agent.ID, ActorID: ids.UserID, ResourceBindingsSet: true, ResourceBindings: []InitialAgentCapabilityInput{binding}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListAgentCapabilities(ctx, agent.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != originalID || rows[0].CapabilityVersionID != version.ID || rows[0].PinningMode != "pinned" || rows[0].Configuration["setting"] != "changed" {
		t.Fatal("binding identity or configuration lost", err)
	}
	_, _, err = st.UpdateAgent(ctx, UpdateAgentInput{AgentID: agent.ID, ActorID: ids.UserID, ResourceBindingsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = st.ListAgentCapabilities(ctx, agent.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("explicit empty selection did not clear bindings", err)
	}
}

func TestAgentCredentialAndSkillSnapshotsStayPrivateAndFrozen(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "resource-encryption-fixture")
	st := New(openTestDB(t))
	ctx := t.Context()
	ids := mustSeedDevFixture(t, ctx, st)
	defaultKey := "provider-key"
	provider, err := st.SaveCatalogProvider(ctx, ids.WorkspaceID, "", CatalogProviderInput{Name: "Provider", Protocol: "anthropic", BaseURL: "https://example.com", APIKey: &defaultKey})
	if err != nil {
		t.Fatal(err)
	}
	model, err := st.CreateCatalogModel(ctx, ids.WorkspaceID, ids.UserID, CatalogModelInput{Name: "Model", ModelKey: "fixture", ProviderID: provider.ID})
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := catalogCipher()
	encrypted, _ := cipher.Encrypt(map[string]any{"value": "selected-key-canary"})
	secret, err := st.CreateSecret(ctx, CreateSecretInput{WorkspaceID: ids.WorkspaceID, Name: "Override", Kind: "capability_inline", CredentialKindCode: "anthropic_api_key"}, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, PrimaryAgentID: ids.BackendAgentID, Title: "Resources"})
	if err != nil {
		t.Fatal(err)
	}
	send, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: conv.ID, UserID: ids.UserID, Content: "test"})
	if err != nil {
		t.Fatal(err)
	}
	request := json.RawMessage(`{"agent":{"model":"fixture","x_agents_core":{"harness":"claude_sdk"}},"environment":{"type":"openai_hosted","skills":[{"source":{"data":"private-archive-canary"}}]}}`)
	selection := map[string]any{"kind": "anthropic_api_key", "source": "shared", "secret_id": secret.ID}
	frozen, err := st.EnsureCoreSessionWithResources(ctx, send.RunIDs[0], request, model.ID, selection, ids.UserID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"selected-key-canary", "private-archive-canary"} {
		if bytes.Contains(frozen.Request, []byte(value)) || bytes.Contains(frozen.ProviderSnapshot, []byte(value)) {
			t.Fatal("private value persisted in clear")
		}
	}
	execution, err := st.CoreSessionProvider(frozen)
	if err != nil || execution.ModelProvider.APIKey != "selected-key-canary" {
		t.Fatal("selected credential did not replace provider default", err)
	}
	environment, err := st.CoreSessionPrivateEnvironment(frozen)
	if err != nil || environment["skills"] == nil {
		t.Fatal("Skill snapshot missing", err)
	}
	repeated, err := st.EnsureCoreSessionWithResources(ctx, send.RunIDs[0], nil, newID(), map[string]any{"invalid": true}, "")
	if err != nil || !bytes.Equal(repeated.ProviderSnapshot, frozen.ProviderSnapshot) {
		t.Fatal("retry reread mutable resources", err)
	}
}

func TestModelCredentialRejectsUnrelatedKindsBeforeLookup(t *testing.T) {
	for _, kind := range []string{"postgres_dsn", "github_pat", "ssh_key", "mcp_oauth", ""} {
		config := map[string]any{"model": "test", "model_credential_binding": map[string]any{"kind": kind, "source": "personal"}}
		if ValidateCoreAgentConfig(config, true) == nil {
			t.Fatalf("accepted unrelated model credential kind %q", kind)
		}
		if _, err := (&Store{}).ResolveAgentCredential(t.Context(), "", "", kind, credentialbinding.Binding{Source: credentialbinding.SourcePersonal}); err == nil {
			t.Fatal("unrelated credential reached execution")
		}
	}
	for _, kind := range []string{"openai_api_key", "anthropic_api_key"} {
		if err := ValidateCoreAgentConfig(map[string]any{"model": "test", "model_credential_binding": map[string]any{"kind": kind, "source": "personal"}}, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentModelCredentialKindsExistWithoutDevFixture(t *testing.T) {
	db := openTestDB(t)
	migration, err := os.ReadFile("../../migrations/000019_agent_model_credential_kinds.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(migration), "-- +goose Down")[0]
	for range 2 {
		if _, err := db.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	st := New(db)
	for _, code := range []string{"openai_api_key", "anthropic_api_key"} {
		kind, err := st.GetCredentialKindByCode(t.Context(), code)
		if err != nil || !kind.BuiltIn || kind.Source != CredentialKindSourcePlatformModel {
			t.Fatalf("model credential kind %q unavailable after migrations: %v", code, err)
		}
	}
}
