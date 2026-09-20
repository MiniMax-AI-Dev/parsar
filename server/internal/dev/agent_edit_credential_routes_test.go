package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// recordingAgentStore overrides stubRuntimeStore.UpdateAgent / GetAgent /
// CreateSecret with pointer-receiver implementations so each test can assert
// on the input the handler forwarded. Visibility is configurable so we can
// exercise the public-agent personal-binding rejection without changing the
// shared fixture's defaults.
type recordingAgentStore struct {
	stubRuntimeStore
	getAgentVisibility string

	lastUpdateInput   store.UpdateAgentInput
	createSecretCalls int
}

func (s *recordingAgentStore) GetAgent(ctx context.Context, agentID string) (store.AgentSummary, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentSummary{}, store.ErrUnknownAgent
	}
	vis := s.getAgentVisibility
	if vis == "" {
		vis = "workspace"
	}
	return store.AgentSummary{
		ID:            agentID,
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		Name:          "Agent",
		Slug:          "agent",
		ConnectorType: "agent_daemon",
		Status:        "active",
		Visibility:    vis,
	}, nil
}

func (s *recordingAgentStore) UpdateAgent(ctx context.Context, input store.UpdateAgentInput) (store.AgentSummary, []string, error) {
	s.lastUpdateInput = input
	name := "Agent"
	if input.Name != nil {
		name = *input.Name
	}
	return store.AgentSummary{
		ID:            input.AgentID,
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		Name:          name,
		Slug:          "agent",
		ConnectorType: "agent_daemon",
		Status:        "active",
		Capabilities:  input.Capabilities,
	}, []string{"name"}, nil
}

func (s *recordingAgentStore) CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error) {
	s.createSecretCalls++
	return s.stubRuntimeStore.CreateSecret(ctx, input, encryptedPayload)
}

// Public Agents cannot resolve personal credentials for external callers.
func TestUpdatePublicAgentRejectsPersonalCredentials(t *testing.T) {
	r := chi.NewRouter()
	rec := &recordingAgentStore{getAgentVisibility: "public"}
	RegisterRoutesWithStore(r, rec)

	body := `{"config":{"credential_bindings":{"gitlab_token":{"source":"personal"}}}}`
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for public + personal, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "gitlab_token") {
		t.Errorf("error message should name the offending kind, got %s", res.Body.String())
	}
	// Nothing should have hit UpdateAgent — the input recorder stays empty.
	if rec.lastUpdateInput.AgentID != "" {
		t.Errorf("UpdateAgent should not have been called, got AgentID=%q", rec.lastUpdateInput.AgentID)
	}
}

// TestUpdateAgentWithoutConfigLeavesConfigSetFalse pins the calling
// contract: a PATCH that doesn't touch config must not set ConfigSet —
// otherwise an unrelated edit (rename) would clobber existing bindings
// via the cherry-pick code path in Store.UpdateAgent.
func TestUpdateAgentWithoutConfigLeavesConfigSetFalse(t *testing.T) {
	r := chi.NewRouter()
	rec := &recordingAgentStore{}
	RegisterRoutesWithStore(r, rec)

	body := `{"name":"Renamed"}`
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	requireStatus(t, res, http.StatusOK)
	if rec.lastUpdateInput.ConfigSet {
		t.Fatalf("ConfigSet should be false when config not in PATCH body, got true")
	}
	if rec.createSecretCalls != 0 {
		t.Fatalf("CreateSecret should not have fired, got %d calls", rec.createSecretCalls)
	}
	// Sanity: the rename actually went through.
	var resp struct {
		Agent struct {
			Name string `json:"name"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Agent.Name != "Renamed" {
		t.Errorf("expected agent renamed, got %q", resp.Agent.Name)
	}
}

// TestUpdateAgentClearsBindingsOnEmptyPayload pins the shared→personal
// clear path: when the FE submits credential_bindings:{} and
// model_credential_binding:null (the user flipped every shared pick back
// to personal), the handler must forward those as ConfigSet=true so
// Store.UpdateAgent can delete the stored keys. Without this the dialog
// silently fails to persist the clear.
