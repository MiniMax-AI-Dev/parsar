package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestHTTPConfigSurvivesCreateAndEdit(t *testing.T) {
	st := New(openTestDB(t))
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	created, err := st.CreateAgent(ctx, CreateAgentInput{WorkspaceID: ids.WorkspaceID, CreatedBy: ids.UserID, Name: "HTTP", ConnectorType: "http", SystemPrompt: "retain instructions", AgentConfig: map[string]any{"http": map[string]any{"endpoint": "https://agent.example.com/invoke"}}})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.GetAgent(ctx, created.Agent.ID)
	if err != nil || HTTPAgentConfigFrom(agent.Config).Endpoint != "https://agent.example.com/invoke" {
		t.Fatalf("lost endpoint: %v", err)
	}
	_, _, err = st.UpdateAgent(ctx, UpdateAgentInput{AgentID: agent.ID, ConfigSet: true, Config: map[string]any{"http": map[string]any{"endpoint": "https://other.example.com/invoke", "secret_id": ""}}})
	if err != nil {
		t.Fatal(err)
	}
	agent, err = st.GetAgent(ctx, agent.ID)
	if err != nil || HTTPAgentConfigFrom(agent.Config).Endpoint != "https://other.example.com/invoke" || agent.Config["system_prompt"] != "retain instructions" {
		t.Fatalf("edit lost config: %v", err)
	}
}
func TestHTTPAgentSecretsRequireWorkspaceOwnershipAndPurpose(t *testing.T) {
	st := New(openTestDB(t))
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	other, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Other", CreatedBy: ids.UserID, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := st.CreateSecret(ctx, CreateSecretInput{WorkspaceID: ids.WorkspaceID, Name: "HTTP", Kind: "http_agent", Provider: "http_agent", AuthType: "bearer"}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetHTTPAgentSecretPayload(ctx, ids.WorkspaceID, secret.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetHTTPAgentSecretPayload(ctx, other.Workspace.ID, secret.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("foreign secret accepted")
	}
	if _, err = st.CreateAgent(ctx, CreateAgentInput{WorkspaceID: other.Workspace.ID, CreatedBy: ids.UserID, Name: "Foreign", ConnectorType: "http", AgentConfig: map[string]any{"http": map[string]any{"endpoint": "https://example.com", "secret_id": secret.ID}}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("foreign binding accepted: %v", err)
	}
	if _, err = st.DisableSecret(ctx, ids.WorkspaceID, secret.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetHTTPAgentSecretPayload(ctx, ids.WorkspaceID, secret.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("disabled secret accepted")
	}
	model, err := st.CreateSecret(ctx, CreateSecretInput{WorkspaceID: ids.WorkspaceID, Name: "Model", Kind: "model_provider", Provider: "http_agent", AuthType: "bearer"}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetHTTPAgentSecretPayload(ctx, ids.WorkspaceID, model.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("model secret accepted")
	}
}
func TestHTTPAgentEndpointValidationAndCanonicalization(t *testing.T) {
	for _, endpoint := range []string{"file:///etc/passwd", "https://user:password@example.com", "https://example.com/#token", "https://"} {
		if ValidHTTPAgentEndpoint(endpoint) {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
	data, err := agentConfigJSON("", "", nil, "", "http", map[string]any{"endpoint": "https://example.com", "secret_id": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	json.Unmarshal(data, &config)
	got := HTTPAgentConfigFrom(config)
	if got.Endpoint != "https://example.com" || got.SecretID != "secret" {
		t.Fatalf("lost legacy config: %+v", got)
	}
}
