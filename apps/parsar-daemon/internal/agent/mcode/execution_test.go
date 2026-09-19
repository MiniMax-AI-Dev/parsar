package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func executionRequest(t *testing.T) proto.PromptRequestPayload {
	r := testRequest(t)
	r.StrictResume, r.ReleaseOnCompletion, r.DisableExecutionEnvironment, r.DisableSubagents = true, true, true, true
	r.ExecutionControls = &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}
	return r
}

func TestExecutionOptionsExcludeAmbientAuthority(t *testing.T) {
	r := executionRequest(t)
	t.Setenv("AGENTS_API_SECRET_CANARY", "secret")
	t.Setenv("NODE_OPTIONS", "--import=untrusted")
	opts, err := prepareOptions(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range opts.Env {
		if strings.HasPrefix(e, "AGENTS_API_SECRET_CANARY=") || strings.HasPrefix(e, "NODE_OPTIONS=") {
			t.Fatal("ambient authority inherited")
		}
	}
	data, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil {
		t.Fatal("bad config")
	}
	features := config["agents"].(map[string]any)["default"].(map[string]any)["features"].(map[string]any)
	for _, k := range []string{"mavis", "delegation", "webSearch"} {
		if features[k] != false {
			t.Fatalf("%s remains enabled", k)
		}
	}
}

func TestExecutionRejectsUnqualifiedAuthority(t *testing.T) {
	for _, change := range []func(*proto.PromptRequestPayload){
		func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = false },
		func(r *proto.PromptRequestPayload) { r.DisableSubagents = false },
		func(r *proto.PromptRequestPayload) { r.RequireExistingNativeSession = true },
		func(r *proto.PromptRequestPayload) { r.WorkDir = "/tmp" },
		func(r *proto.PromptRequestPayload) { r.FunctionTools = []proto.FunctionTool{{Name: "f"}} },
		func(r *proto.PromptRequestPayload) { r.ExecutionControls.WebSearch = "enabled" },
		func(r *proto.PromptRequestPayload) { r.AgentOptions["env"] = map[string]any{"X": "Y"} },
	} {
		r := executionRequest(t)
		change(&r)
		if _, err := prepareOptions(t.Context(), r); err == nil {
			t.Fatal("unsupported execution accepted")
		}
	}
}

func TestPublicTextDoesNotInvokeACPCommands(t *testing.T) {
	for _, text := range []string{"/model", "/compact", "hello"} {
		blocks := promptContent(text, true)
		if len(blocks) != 2 || blocks[0]["text"] != text || blocks[1]["text"] != "" {
			t.Fatal(blocks)
		}
		if blocks = promptContent(text, false); len(blocks) != 1 || blocks[0]["text"] != text {
			t.Fatal("product prompt changed")
		}
	}
}
