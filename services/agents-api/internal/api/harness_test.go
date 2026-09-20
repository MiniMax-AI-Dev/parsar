package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSessionHarnessAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, extension, extra, environment, engine string
		enabled                                     bool
		status                                      int
	}{
		{"default", "", "", `{"type":"none"}`, "codex", false, 200},
		{"explicit default", `,"x_agents_core":{"harness":"codex"}`, "", `{"type":"none"}`, "codex", false, 200},
		{"claude", `,"x_agents_core":{"harness":"claude_sdk"}`, "", `{"type":"none"}`, "claude_sdk", true, 200},
		{"mcode", `,"x_agents_core":{"harness":"mcode"}`, "", `{"type":"none"}`, "mcode", true, 200},
		{"unavailable", `,"x_agents_core":{"harness":"claude_sdk"}`, "", `{"type":"none"}`, "", false, 400},
		{"unknown", `,"x_agents_core":{"harness":"other"}`, "", `{"type":"none"}`, "", true, 400},
		{"empty", `,"x_agents_core":{}`, "", `{"type":"none"}`, "", true, 400},
		{"unknown nested", `,"x_agents_core":{"harness":"codex","model":"wrong"}`, "", `{"type":"none"}`, "", true, 400},
		{"claude verbosity", `,"x_agents_core":{"harness":"claude_sdk"}`, `,"text":{"verbosity":"high"}`, `{"type":"none"}`, "", true, 400},
		{"mcode remote", `,"x_agents_core":{"harness":"mcode"}`, "", `{"type":"self_hosted","workspace_directory":"/workspace"}`, "", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var options []Option
			if tc.enabled {
				options = append(options, WithHarnesses([]string{"claude_sdk", "mcode"}))
			}
			h, s, _ := testHandler(t, options...)
			r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"fixture"`+tc.extension+tc.extra+`},"environment":`+tc.environment+`}`))
			r.Header.Set("Authorization", "Bearer test-api-key")
			r.Header.Set("OpenAI-Beta", "agents=v1")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || s.input.Engine != tc.engine {
				t.Fatalf("status=%d body=%s engine=%s", w.Code, w.Body, s.input.Engine)
			}
		})
	}
}

func TestSavedHarnessReplacementAndEffectiveRead(t *testing.T) {
	model := "fixture"
	resolved, err := resolveSavedAgent(v1.CreateAgentRequest{Model: &model, XAgentsCore: &v1.AgentsCore{Harness: "claude_sdk"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg v1.SavedAgentConfiguration
	if err := json.Unmarshal(resolved.Configuration, &cfg); err != nil {
		t.Fatal(err)
	}
	saved := &v1.SavedAgent{SavedAgentConfiguration: cfg, ID: "agent-saved"}
	for _, tc := range []struct{ raw, want string }{
		{`{}`, "claude_sdk"}, {`{"x_agents_core":{"harness":"mcode"}}`, "mcode"}, {`{"x_agents_core":null}`, ""},
	} {
		var request decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"agent_id":"agent-saved","agent":`+tc.raw+`,"environment":{"type":"none"}}`), &request); err != nil {
			t.Fatal(err)
		}
		input, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		agent, err := resolveSessionAgent(input, saved)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if agent.XAgentsCore != nil {
			got = agent.XAgentsCore.Harness
		}
		if got != tc.want {
			t.Fatalf("%s: %q", tc.raw, got)
		}
	}
	update, err := resolveAgentUpdate([]byte(`{"x_agents_core":null}`))
	if err != nil || string(update.Configuration) != `{"x_agents_core":null}` {
		t.Fatalf("null clear=%s %v", update.Configuration, err)
	}
	if saved.XAgentsCore.Harness != "claude_sdk" {
		t.Fatal("mutated saved Agent")
	}
	raw, _ := json.Marshal(configuration{Agent: v1.Agent{ID: "agent", Model: "fixture", XAgentsCore: &v1.AgentsCore{Harness: "claude_sdk"}}, Environment: v1.Environment{Type: "none"}})
	response, err := sessionResponse(store.Session{Engine: "mcode", Configuration: raw}, "")
	if err != nil || response.Agent.XAgentsCore.Harness != "mcode" {
		t.Fatalf("effective read=%+v %v", response, err)
	}
}

func TestDefaultHarnessPreservesSessionAgentResponse(t *testing.T) {
	raw, _ := json.Marshal(configuration{Agent: v1.Agent{ID: "agent", Model: "fixture"}, Environment: v1.Environment{Type: "none"}})
	response, err := sessionResponse(store.Session{Engine: "codex", Configuration: raw}, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response.Agent)
	if err != nil || response.Agent.XAgentsCore != nil || strings.Contains(string(encoded), "x_agents_core") {
		t.Fatalf("default selection changed the protocol Agent: %s %v", encoded, err)
	}
}
