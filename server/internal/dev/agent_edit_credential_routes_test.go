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

// TestUpdateAgentPersistsCredentialBindings verifies the PATCH /agents/{id}
// path now accepts config.credential_bindings + inline_new_secrets and
// forwards them to Store.UpdateAgent with ConfigSet=true. Without this the
// edit dialog can't change the agent's shared secret — which is the bug
// users originally reported (radio "shared" stays empty even after picking).
func TestUpdateAgentPersistsCredentialBindings(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-master-key")
	r := chi.NewRouter()
	rec := &recordingAgentStore{}
	RegisterRoutesWithStore(r, rec)

	body := `{
        "config": {
            "credential_bindings": {
                "gitlab_token": {"source": "shared", "secret_id": "00000000-0000-0000-0000-0000000000aa"}
            }
        },
        "inline_new_secrets": [
            {"kind": "gitlab_token", "display_name": "team token", "plaintext": "glpat-xxxx"}
        ]
    }`
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !rec.lastUpdateInput.ConfigSet {
		t.Fatalf("expected ConfigSet=true on UpdateAgentInput, got false")
	}
	bindings, ok := rec.lastUpdateInput.Config["credential_bindings"].(map[string]any)
	if !ok {
		t.Fatalf("expected Config.credential_bindings map, got %#v", rec.lastUpdateInput.Config["credential_bindings"])
	}
	gitlab, ok := bindings["gitlab_token"].(map[string]any)
	if !ok {
		t.Fatalf("expected gitlab_token binding map, got %#v", bindings["gitlab_token"])
	}
	if gitlab["source"] != "shared" {
		t.Fatalf("expected source=shared, got %v", gitlab["source"])
	}
	// Inline secret should have been materialised once and the resulting
	// secret_id stamped onto the binding for that kind.
	if rec.createSecretCalls != 1 {
		t.Fatalf("expected exactly 1 CreateSecret call, got %d", rec.createSecretCalls)
	}
}

// TestUpdateAgentRejectsPersonalBindingOnPublicAgent guards the public-agent
// invariant: lark guests have no platform user_id, so personal credentials
// can't resolve at dispatch. The handler must 422 before writing anything.
func TestUpdateAgentRejectsPersonalBindingOnPublicAgent(t *testing.T) {
	r := chi.NewRouter()
	rec := &recordingAgentStore{getAgentVisibility: "public"}
	RegisterRoutesWithStore(r, rec)

	body := `{"config":{"credential_bindings":{"gitlab_token":{"source":"personal"}}}}`
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for public + personal, got %d: %s", res.Code, res.Body.String())
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

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
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
func TestUpdateAgentClearsBindingsOnEmptyPayload(t *testing.T) {
	r := chi.NewRouter()
	rec := &recordingAgentStore{}
	RegisterRoutesWithStore(r, rec)

	body := `{"config":{"credential_bindings":{},"model_credential_binding":null}}`
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !rec.lastUpdateInput.ConfigSet {
		t.Fatalf("expected ConfigSet=true on clear payload, got false")
	}
	cb, hasCB := rec.lastUpdateInput.Config["credential_bindings"]
	if !hasCB {
		t.Fatalf("credential_bindings key must be forwarded (even when empty) so the store can delete the stored value")
	}
	if m, ok := cb.(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("expected credential_bindings to be an empty object, got %#v", cb)
	}
	mb, hasMB := rec.lastUpdateInput.Config["model_credential_binding"]
	if !hasMB {
		t.Fatalf("model_credential_binding key must be forwarded (even when null) so the store can delete the stored value")
	}
	if mb != nil {
		t.Fatalf("expected model_credential_binding to be nil, got %#v", mb)
	}
}
