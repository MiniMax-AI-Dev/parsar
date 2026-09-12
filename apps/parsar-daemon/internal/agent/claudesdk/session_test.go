//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTextFactoryCompletionAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "wrong-resume", "missing", "malformed", "process-failed", "after-result", "bridge-error"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=" + mode, "GORACE=atexit_sleep_ms=0"}}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			s, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			failed := false
			done := false
			deltas := ""
			for event := range out {
				if event.ID != "run" {
					t.Fatal("wrong run identity")
				}
				switch event.Type {
				case proto.TypeDelta:
					var payload proto.DeltaPayload
					_ = json.Unmarshal(event.Payload, &payload)
					deltas += payload.Delta
				case proto.TypeError:
					failed = true
				case proto.TypeDone:
					done = true
					var payload proto.DonePayload
					_ = json.Unmarshal(event.Payload, &payload)
					if mode == "success" {
						if payload.Content != "final" || payload.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || deltas != "partial" {
							t.Fatalf("bad completion: %+v, deltas %q", payload, deltas)
						}
						if _, err := os.Stat(filepath.Join(config.StateDir, "released")); err != nil {
							t.Fatal("Done preceded process release")
						}
					} else if payload.Metadata[proto.DoneMetaAgentSessionID] != nil {
						t.Fatal("failed result exposed a successful continuity id")
					}
				}
			}
			if !done || failed != (mode != "success") {
				t.Fatalf("done=%t failed=%t", done, failed)
			}
		})
	}
}

func TestTextFactoryRejectsUnsupportedInput(t *testing.T) {
	for _, kind := range []string{"tool", "option", "relative", "outside"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", AgentOptions: map[string]any{"model": "fake"}}
			switch kind {
			case "tool":
				request.FunctionTools = []proto.FunctionTool{{}}
			case "option":
				request.AgentOptions["allowed_tools"] = "anything"
			case "relative":
				request.WorkDir = "relative"
			case "outside":
				config.StateDir = filepath.Dir(root)
			}
			_, err := NewFactory(config)(context.Background(), request, make(chan proto.Envelope, 1))
			if err == nil || !strings.HasPrefix(err.Error(), "claudesdk:") {
				t.Fatalf("expected pre-launch rejection, got %v", err)
			}
		})
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_CLAUDE_SDK_HELPER") == "1" {
		runSDKHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runSDKHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(2)
	}
	var request startRequest
	if json.Unmarshal(scanner.Bytes(), &request) != nil || request.Type != "start" || request.Prompt != "hello" || request.Model != "fake-model" || request.SystemPrompt != "instructions" {
		os.Exit(3)
	}
	encode := func(event bridgeEvent) { _ = json.NewEncoder(os.Stdout).Encode(event) }
	mode := os.Getenv("SDK_HELPER_MODE")
	switch mode {
	case "missing":
		return
	case "malformed":
		fmt.Fprintln(os.Stdout, "malformed")
		time.Sleep(time.Hour)
		return
	case "bridge-error":
		encode(bridgeEvent{Type: "error", Code: "execution_failed"})
		return
	}
	encode(bridgeEvent{Type: "delta", Delta: "partial"})
	id := request.Resume
	if mode == "wrong-resume" {
		id = "different-session"
	}
	encode(bridgeEvent{Type: "result", Text: "final", SessionID: id})
	if mode == "process-failed" {
		os.Exit(7)
	}
	if mode == "after-result" {
		encode(bridgeEvent{Type: "delta", Delta: "too late"})
		return
	}
	time.Sleep(50 * time.Millisecond)
	_ = os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "released"), nil, 0o600)
}
