package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func TestPublicFunctionConfiguration(t *testing.T) {
	tool := `{"type":"function","name":"lookup","description":"","parameters":{"const":9007199254740993}}`
	for _, suffix := range []string{"", `,"tools":null`, `,"tools":[]`, `,"tools":[` + tool + `]`, `,"tools":[` + strings.TrimSuffix(tool, "}") + `,"defer_loading":false}]`} {
		h, s, _ := testHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"model"`+suffix+`},"environment":{"type":"none"}}`))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(suffix, w.Code, w.Body)
		}
		var response v1.Session
		var saved configuration
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || json.Unmarshal(s.input.Configuration, &saved) != nil {
			t.Fatal(w.Body)
		}
		if len(response.Agent.Tools) != len(saved.Agent.Tools) || response.Agent.Tools == nil {
			t.Fatal(response.Agent.Tools, saved.Agent.Tools)
		}
		if strings.Contains(suffix, "lookup") {
			if len(response.Agent.Tools) != 1 || !strings.Contains(string(saved.Agent.Tools[0]), `9007199254740993`) || !strings.Contains(string(saved.Agent.Tools[0]), `"defer_loading":false`) {
				t.Fatal(saved.Agent.Tools)
			}
		} else if len(response.Agent.Tools) != 0 {
			t.Fatal(response.Agent.Tools)
		}
	}
}

func TestPublicFunctionConfigurationRejectsInvalidOrUnsupported(t *testing.T) {
	base := `"type":"function","name":"lookup","description":"","parameters":{}`
	for _, raw := range []string{
		`[null]`, `[{}]`, `[{"type":"web_search"}]`, `[{"type":"function","name":"lookup","parameters":{}}]`,
		`[{"type":"function","name":null,"description":"","parameters":{}}]`,
		`[{"type":"function","name":"lookup","description":null,"parameters":{}}]`,
		`[{"type":"function","name":"lookup","description":""}]`,
		`[{"type":"function","name":"lookup","description":"","parameters":null}]`,
		`[{"type":"function","name":"lookup","description":"","parameters":[]}]`,
		`[{` + base + `,"defer_loading":null}]`, `[{` + base + `,"defer_loading":true}]`, `[{` + base + `,"defer_loading":"false"}]`,
		`[{` + base + `,"unexpected":true}]`, `[{` + base + `},{` + base + `}]`,
	} {
		h, s, _ := testHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"model","tools":`+raw+`},"environment":{"type":"none"}}`))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 || s.tenant != "" {
			t.Fatal(raw, w.Code, w.Body)
		}
	}
}
