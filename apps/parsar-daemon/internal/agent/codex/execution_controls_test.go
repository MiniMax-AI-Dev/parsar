package codex

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestExecutionControlsOverrideWithoutMutatingNativeOptions(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	original := map[string]any{"model": "test-model", "web_search": "live", "model_verbosity": "high"}
	request := proto.PromptRequestPayload{AgentOptions: original}
	if got := executionOptions(request); !reflect.DeepEqual(got, original) {
		t.Fatal("ordinary options changed")
	}
	for _, search := range []string{"disabled", "cached", "live"} {
		for _, verbosity := range []string{"low", "medium", "high"} {
			request.ExecutionControls = &proto.ExecutionControls{WebSearch: search, TextVerbosity: verbosity}
			options := executionOptions(request)
			if options["model"] != "test-model" || original["web_search"] != "live" || original["model_verbosity"] != "high" {
				t.Fatal("operator options mutated")
			}
			plan, err := BuildSessionPlan("run", "state", "", options)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"web_search": `"` + search + `"`, "model_verbosity": `"` + verbosity + `"`}
			for _, kv := range plan.ExtraConfig {
				if v, ok := want[kv[0]]; ok {
					if kv[1] != v {
						t.Fatal("native control differs", kv)
					}
					delete(want, kv[0])
				}
			}
			plan.Cleanup()
			if len(want) != 0 {
				t.Fatal("native settings omitted", want)
			}
		}
	}
}

func TestExecutionControlsRejectIncompleteOrInvalidValues(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	for _, controls := range []proto.ExecutionControls{
		{}, {WebSearch: "disabled"}, {TextVerbosity: "medium"},
		{WebSearch: "invalid", TextVerbosity: "medium"}, {WebSearch: "disabled", TextVerbosity: "invalid"},
	} {
		options := executionOptions(proto.PromptRequestPayload{ExecutionControls: &controls})
		if plan, err := BuildSessionPlan("run", "state", "", options); err == nil {
			plan.Cleanup()
			t.Fatal("invalid controls accepted", controls)
		}
	}
}
