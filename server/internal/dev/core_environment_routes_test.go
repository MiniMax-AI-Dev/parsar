package dev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/go-chi/chi/v5"
	"github.com/openai/openai-go/v3"
)

func TestCoreTemplateForwardsCompleteContractAfterAuthorization(t *testing.T) {
	var payload map[string]any
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/agents/environments/templates" || r.Header.Get("OpenAI-Beta") != "agents=v1" {
			t.Errorf("wrong contract: %s", r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tmpl-one","object":"agent.environment.template","name":"Research","created_at":1000,"network":{"access":"restricted","allowed_domains":["example.com"]},"packages":{"python":["pandas"]}}`))
	}))
	defer upstream.Close()
	client, err := agentsclient.NewAgents(agentsclient.Config{BaseURL: upstream.URL + "/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"Research","network":{"access":"restricted","allowed_domains":["example.com"]},"packages":{"python":["pandas"],"npm":["typescript"],"system":["git"]},"setup_commands":[{"command":"echo confidential-value","cwd":"/workspace"}],"env":{"TOKEN":"private-value"},"files":[],"skills":[],"plugins":[],"capability_directories":[]}`
	for _, role := range []string{"not_member", "viewer", "owner"} {
		router := chi.NewRouter()
		RegisterRoutesWithStore(router, agentDetailRouteStore{role: role}, WithCoreAccess(func(id string) (openai.BetaAgentService, error) {
			if id != testWorkspaceID {
				t.Fatal("wrong workspace")
			}
			return client, nil
		}))
		req := withTestUser(httptest.NewRequest("POST", "/api/v1/workspaces/"+testWorkspaceID+"/core/environments/templates", strings.NewReader(body)))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if role != "owner" {
			if calls != 0 || res.Code < 400 {
				t.Fatalf("unauthorized upstream access: %s %d", role, res.Code)
			}
			continue
		}
		if res.Code != 201 {
			t.Fatalf("create: %d %s", res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "private-value") || strings.Contains(res.Body.String(), "confidential-value") {
			t.Fatal("template secrets were echoed")
		}
	}
	if calls != 1 || payload["env"].(map[string]any)["TOKEN"] != "private-value" || payload["setup_commands"].([]any)[0].(map[string]any)["cwd"] != "/workspace" {
		t.Fatalf("complete configuration was dropped: %v", payload)
	}
}

func TestLegacyExecutionRoutesAreRemoved(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutesWithStore(router, stubRuntimeStore{})
	for _, path := range []string{"/api/v1/models", "/api/v1/runtimes", "/api/v1/workspaces/" + testWorkspaceID + "/runtimes", "/agent-daemon/ws", "/api/v1/agent-runs/00000000-0000-0000-0000-000000000901/requeue"} {
		for _, method := range []string{"GET", "POST"} {
			res := httptest.NewRecorder()
			router.ServeHTTP(res, withTestUser(httptest.NewRequest(method, path, nil)))
			if res.Code != 404 && res.Code != 405 {
				t.Errorf("legacy %s %s remains registered: %d", method, path, res.Code)
			}
		}
	}
}

func TestSessionCreationRejectsUnknownEnvironmentFields(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutesWithStore(router, agentDetailRouteStore{role: "owner"})
	body := `{"environment":{"type":"openai_hosted","env":{"TOKEN":"private"}}}`
	req := withTestUser(httptest.NewRequest("POST", "/api/v1/workspaces/"+testWorkspaceID+"/conversations", strings.NewReader(body)))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("silently accepted inline environment secrets: %d", res.Code)
	}
}
