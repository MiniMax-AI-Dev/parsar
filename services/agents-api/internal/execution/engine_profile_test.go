package execution

import (
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestAcceptedEnginePlacements(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "unregistered"} {
		for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"` + placement + `"`)
			if placement == "self_hosted" {
				raw = append(raw, []byte(`,"workspace_directory":"/work"`)...)
			}
			raw = append(raw, []byte(`}}`)...)
			want := engine != "unregistered" && !(engine == "claude_sdk" && placement == "self_hosted")
			if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != want {
				t.Fatalf("%s/%s: %v", engine, placement, err)
			}
		}
	}
}

func TestAdditionalProfileUsesCommonAdmission(t *testing.T) {
	configurationChecked, toolsChecked, resultChecked := false, false, false
	catalog := engine.NewCatalog(map[string]engine.Profile{"fixture": {
		Placements: []string{"none"},
		ValidateConfiguration: func(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error {
			configurationChecked = true
			if agent.Model != "fixture" || environment == nil || hasDaemon {
				return engine.ErrInvalidInput
			}
			return nil
		},
		ValidateTools: func(_ *v1.Environment, _ bool, tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			toolsChecked = true
			if len(tools) != 1 || tools[0].Name != "echo" || len(mcp) != 0 {
				t.Fatal("common tool decoding did not reach profile")
			}
			return nil
		},
		ValidateFunctionResult: func(content []proto.FunctionResultContent) error {
			resultChecked = true
			if len(content) != 1 || content[0].Text == nil || *content[0].Text != "response" {
				t.Fatal("common result decoding did not reach profile")
			}
			return engine.ErrInvalidInput
		},
	}})
	profile, ok := catalog.Lookup("fixture")
	if !ok || !profile.Accepts("none") || profile.Accepts("openai_hosted") {
		t.Fatal("fixture qualification was not selected")
	}
	snapshot := Snapshot{Agent: v1.Agent{Model: "fixture", Tools: []json.RawMessage{json.RawMessage(`{"type":"function","name":"echo","parameters":{"type":"object"}}`)}}, Environment: &v1.Environment{Type: "none"}}
	if err := validateProfileConfiguration(profile, snapshot); err != nil || !configurationChecked || !toolsChecked {
		t.Fatal("additional engine admission failed", err)
	}
	snapshot.Agent.Model = "invalid"
	if err := validateProfileConfiguration(profile, snapshot); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("profile configuration lost public error mapping", err)
	}
	inputs := []store.Input{{Kind: "tool_result", Payload: json.RawMessage(`{"call_id":"call","result":{"success":true,"output":"response"}}`)}}
	if err := validateProfileInputs(profile, inputs); !errors.Is(err, store.ErrInvalidInput) || !resultChecked {
		t.Fatal("profile result lost public error mapping", err)
	}
}

func TestClaudeHostedToolsRequireSeparateQualification(t *testing.T) {
	for index, tools := range []string{`[{"type":"function","name":"f","parameters":{"type":"object"},"defer_loading":false}]`, `[{"type":"mcp","server_label":"s","transport":{"type":"http","server_url":"https://example.test/mcp"},"connection_origin":"service"}]`} {
		for _, placement := range []string{"none", "openai_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture","tools":` + tools + `},"environment":{"type":"` + placement + `"}}`)
			if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", raw); (err == nil) != (placement == "none" || index == 0) {
				t.Fatalf("%s: %v", placement, err)
			}
		}
	}
}
