package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type agentDetailRouteStore struct {
	stubRuntimeStore
	agent store.AgentSummary
	role  string
}

func (s agentDetailRouteStore) GetAgent(context.Context, string) (store.AgentSummary, error) {
	return s.agent, nil
}

func (s agentDetailRouteStore) GetWorkspaceMemberRole(context.Context, string, string) (string, error) {
	if s.role == "" {
		return "owner", nil
	}
	if s.role == "not_member" {
		return "", store.ErrNotMember
	}
	return s.role, nil
}

func TestGetWorkspaceAgentReturnsCompleteSummary(t *testing.T) {
	workspaceID := testWorkspaceID
	agentID := "00000000-0000-0000-0000-000000000901"
	runtimeStore := agentDetailRouteStore{
		agent: store.AgentSummary{
			ID:            agentID,
			WorkspaceID:   workspaceID,
			Name:          "Research Agent",
			Slug:          "research-agent",
			Description:   "Researches and summarizes findings",
			ConnectorType: "agent_daemon",
			Visibility:    "workspace",
			Status:        "active",
			Capabilities:  []string{"web_search"},
			Config: map[string]any{
				"agent_kind": "codex",
				"model_id":   "gpt-5",
				"workdir":    "/workspace",
			},
		},
	}

	r := testRouterForAgentDetail(runtimeStore)
	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/agents/"+agentID, nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var got store.AgentSummary
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != agentID || got.WorkspaceID != workspaceID || got.Config["agent_kind"] != "codex" {
		t.Fatalf("unexpected agent detail: %+v", got)
	}
}

func TestGetWorkspaceAgentRejectsNonMember(t *testing.T) {
	workspaceID := testWorkspaceID
	agentID := "00000000-0000-0000-0000-000000000901"
	r := testRouterForAgentDetail(agentDetailRouteStore{
		stubRuntimeStore: stubRuntimeStore{},
		agent:            store.AgentSummary{ID: agentID, WorkspaceID: workspaceID},
		role:             "not_member",
	})
	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/agents/"+agentID, nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-member, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "not a member") {
		t.Fatalf("expected membership error, got %s", res.Body.String())
	}
}

func TestGetWorkspaceAgentHidesCrossWorkspaceAgent(t *testing.T) {
	workspaceID := testWorkspaceID
	agentID := "00000000-0000-0000-0000-000000000901"
	r := testRouterForAgentDetail(agentDetailRouteStore{
		agent: store.AgentSummary{ID: agentID, WorkspaceID: "00000000-0000-0000-0000-000000000003"},
	})
	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/agents/"+agentID, nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-workspace agent, got %d: %s", res.Code, res.Body.String())
	}
}

func testRouterForAgentDetail(runtimeStore RuntimeStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, runtimeStore)
	return r
}
