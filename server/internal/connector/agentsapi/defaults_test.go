package agentsapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

func TestAgentDefaultsReachCoreWithoutMetadata(t *testing.T) {
	st := &memoryStore{}
	config := map[string]any{"model": "fixture", "x_agents_core": map[string]any{"harness": "mcode"}, "environment": map[string]any{"type": "openai_hosted", "environment_template_id": "template"}}
	request, err := (&Connector{store: st}).sessionRequest(context.Background(), connector.PromptInput{AgentConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var agent map[string]any
	_ = json.Unmarshal(fields["agent"], &agent)
	var environment map[string]any
	_ = json.Unmarshal(fields["environment"], &environment)
	if agent["x_agents_core"].(map[string]any)["harness"] != "mcode" || agent["environment"] != nil || environment["environment_template_id"] != "template" {
		t.Fatalf("wrong defaults: %s", raw)
	}
	st.metadata = map[string]any{"core_environment": map[string]any{"type": "none"}}
	request, err = (&Connector{store: st}).sessionRequest(context.Background(), connector.PromptInput{AgentConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(request)
	_ = json.Unmarshal(raw, &fields)
	_ = json.Unmarshal(fields["environment"], &environment)
	if environment["type"] != "none" {
		t.Fatalf("explicit conversation environment lost: %s", raw)
	}
}
