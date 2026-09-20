package agentsapi

import (
	"context"
	"encoding/json"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type catalogMemoryStore struct {
	*memoryStore
	modelID   string
	persisted json.RawMessage
}

func (s *catalogMemoryStore) EnsureCoreSessionWithModel(ctx context.Context, run string, raw json.RawMessage, modelID string) (store.CoreSessionBinding, error) {
	s.modelID = modelID
	s.persisted = raw
	binding, err := s.EnsureCoreSession(ctx, run, raw)
	binding.ProviderSnapshot = []byte("encrypted-snapshot")
	s.session = binding
	return binding, err
}
func (s *catalogMemoryStore) CoreSessionProvider(store.CoreSessionBinding) (*v1.SessionExecutionInput, error) {
	return &v1.SessionExecutionInput{ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "connector-private-canary"}}, nil
}
func TestCatalogConnectorAddsWriteOnlyExecutionAfterSnapshot(t *testing.T) {
	st := &catalogMemoryStore{memoryStore: &memoryStore{}}
	upstream := &protocolServer{t: t, keys: map[string]bool{}, status: "completed"}
	received := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/agents/sessions" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			var request map[string]json.RawMessage
			if json.Unmarshal(raw, &request) != nil || !strings.Contains(string(request["x_agents_core"]), "connector-private-canary") {
				t.Error("missing confidential execution extension")
			}
			if strings.Contains(string(request["agent"]), "model_id") {
				t.Error("product catalog identity sent to Core Agent")
			}
			received = true
		}
		upstream.ServeHTTP(w, r)
	}))
	defer server.Close()
	c, err := New(agentsclient.Config{BaseURL: server.URL + "/v1", APIKey: "test-core-key"}, st)
	if err != nil {
		t.Fatal(err)
	}
	in := testInput()
	in.AgentConfig["model_id"] = "product-model-id"
	events, err := c.StreamPrompt(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Persisted != nil {
			event.Persisted <- nil
		}
		if event.Error != "" {
			t.Error(event.Error)
		}
	}
	if !received || st.modelID != "product-model-id" || strings.Contains(string(st.persisted), "connector-private-canary") {
		t.Fatal("credential transport or persistence boundary failed")
	}
}
