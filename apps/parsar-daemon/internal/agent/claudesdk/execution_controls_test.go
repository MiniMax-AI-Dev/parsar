//go:build unix

package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestExecutionControlsPreserveNativeDefaultsAndInstructions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Prompt: "Original input.", AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "native-model", "system_prompt": "Keep these exact instructions.\nDo not replace them."}}
	ordinary, _, err := prepare(config, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionControls = &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}
	before, _ := json.Marshal(request)
	controlled, _, err := prepare(config, request)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if !reflect.DeepEqual(ordinary, controlled) || string(before) != string(after) {
		t.Fatal("default controls changed native input, instructions, continuation or caller options")
	}
}

func TestExecutionControlsRejectUnsupportedProfilesBeforeLaunch(t *testing.T) {
	cases := map[string]proto.ExecutionControls{
		"empty": {}, "missing-search": {TextVerbosity: "medium"}, "missing-verbosity": {WebSearch: "disabled"},
		"cached-search":     {WebSearch: "cached", TextVerbosity: "medium"},
		"live-search":       {WebSearch: "live", TextVerbosity: "medium"},
		"unknown-search":    {WebSearch: "invalid", TextVerbosity: "medium"},
		"low-verbosity":     {WebSearch: "disabled", TextVerbosity: "low"},
		"high-verbosity":    {WebSearch: "disabled", TextVerbosity: "high"},
		"unknown-verbosity": {WebSearch: "disabled", TextVerbosity: "invalid"},
	}
	for name, controls := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "Original input.", ExecutionControls: &controls, AgentOptions: map[string]any{"model": "native-model"}}
			_, err := NewFactory(config)(t.Context(), request, make(chan proto.Envelope, 1))
			if err == nil || !strings.Contains(err.Error(), "execution controls require") {
				t.Fatal("unsupported controls did not fail at admission", err)
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("unsupported controls reached native setup", err)
			}
		})
	}
}
