package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestTemplateConfigurationRejectsUnqualifiedInputs(t *testing.T) {
	for _, raw := range []string{`{}`, `{"packages":{}}`, `{"packages":{"npm":null}}`, `{"name":null,"network":null}`, `{"name":"保存","network":{"access":"disabled"},"env":{},"files":[],"setup_commands":[],"packages":{"npm":null}}`} {
		if _, err := decodeTemplateInput([]byte(raw)); err != nil {
			t.Fatalf("supported input: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"name":""}`, `{"name":42}`, `{"type":"openai_hosted"}`, `{"network":{"access":"restricted","allowed_domains":["example.com"]}}`, `{"env":{"PATH":"confidential-canary"}}`, `{"setup_commands":[{"command":"confidential-canary","cwd":"relative"}]}`, `{"packages":{"system":["curl"]}}`, `{"plugins":[{}]}`, `{"skills":[{}]}`, `{"capability_directories":["/workspace"]}`} {
		if _, err := decodeTemplateInput([]byte(raw)); err == nil {
			t.Fatalf("unsupported input accepted: %s", raw)
		}
	}
	h, _, _ := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/environments/templates", strings.NewReader(`{"env":{"PATH":"confidential-canary"}}`))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("OpenAI-Beta", "agents=v1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "confidential-canary") {
		t.Fatal("confidential input not safely rejected", response.Code, response.Body.String())
	}
}

type templateLookupStore struct {
	ResourceStore
	network string
	tenant  string
}

func (s *templateLookupStore) ResolveEnvironmentTemplate(_ context.Context, tenant, id string) (store.EnvironmentTemplate, []store.InitialFile, error) {
	s.tenant = tenant
	return store.EnvironmentTemplate{ID: id, NetworkAccess: s.network}, nil, nil
}

func TestTemplateResolutionAndCreationIntent(t *testing.T) {
	lookup := &templateLookupStore{network: "disabled"}
	h := Handler{store: lookup}
	request := func(raw string) sessionRequest {
		t.Helper()
		var decoded decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"agent":{"model":"test"},"environment":`+raw+`}`), &decoded); err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	inherited := request(`{"type":"openai_hosted","environment_template_id":"saved"}`)
	intent, err := sessionCreationRequest(inherited, nil)
	if err != nil || !strings.Contains(string(intent), `"environment_template_id":"saved"`) {
		t.Fatal("missing caller intent", string(intent), err)
	}
	if err := h.resolveTemplateEnvironment(t.Context(), "tenant-a", &inherited); err != nil || inherited.Environment.Network.Access != "disabled" || lookup.tenant != "tenant-a" {
		t.Fatal("inheritance failed", err)
	}
	raw, _ := json.Marshal(inherited.Environment)
	if strings.Contains(string(raw), "template") {
		t.Fatal("template leaked to execution", string(raw))
	}
	broader := request(`{"type":"openai_hosted","environment_template_id":"saved","network":{"access":"enabled"}}`)
	broaderIntent, _ := sessionCreationRequest(broader, nil)
	if string(broaderIntent) == string(intent) {
		t.Fatal("default erased caller override")
	}
	if err := h.resolveTemplateEnvironment(t.Context(), "tenant-a", &broader); err == nil {
		t.Fatal("network broadened")
	}
	narrower := request(`{"type":"openai_hosted","environment_template_id":"saved","network":{"access":"disabled"}}`)
	lookup.network = "enabled"
	if err := h.resolveTemplateEnvironment(t.Context(), "tenant-a", &narrower); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"type":"openai_hosted","environment_template_id":null}`, `{"type":"none","environment_template_id":"saved"}`, `{"type":"openai_hosted","environment_template_id":"saved","network":null}`, `{"type":"openai_hosted","environment_template_id":"saved","env":{"KEY":"secret"}}`} {
		if _, _, _, err := decodeTemplateEnvironment(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid reference accepted", raw)
		}
	}
}
