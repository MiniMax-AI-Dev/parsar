//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const usageFixture = `{"subtype":"success","is_error":false,"usage":{"input_tokens":12,"output_tokens":4},"modelUsage":{"first":{"inputTokens":12,"outputTokens":4,"costUSD":0.1,"costBasis":"unknown"},"second":{"inputTokens":25,"outputTokens":5}},"total_cost_usd":0.1}`

func TestUsageTransportPreservesSnapshotOnFailureAndDone(t *testing.T) {
	for _, mode := range []string{"success", "native-error", "process-error", "missing", "malformed", "duplicate", "wrong-session", "changed-result"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=usage-" + mode, "GORACE=atexit_sleep_ms=0"}}
			request := proto.PromptRequestPayload{RunID: "usage-run", Prompt: "hello", AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			s, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			var observed proto.Usage
			var done proto.DonePayload
			var kinds []string
			for event := range out {
				if event.ID != request.RunID {
					t.Fatal("usage escaped run identity")
				}
				kinds = append(kinds, event.Type)
				switch event.Type {
				case proto.TypeUsage:
					var value proto.UsagePayload
					if err := event.DecodePayload(&value); err != nil {
						t.Fatal(err)
					}
					observed = value.Usage
				case proto.TypeDone:
					if err := event.DecodePayload(&done); err != nil {
						t.Fatal(err)
					}
				}
			}
			expected := []string{proto.TypeUsage, proto.TypeDone}
			if mode == "missing" {
				expected = []string{proto.TypeDone}
			}
			if mode == "native-error" || mode == "process-error" || mode == "duplicate" || mode == "changed-result" {
				expected = []string{proto.TypeUsage, proto.TypeError, proto.TypeDone}
			}
			if mode == "malformed" || mode == "wrong-session" {
				expected = []string{proto.TypeError, proto.TypeDone}
			}
			if !reflect.DeepEqual(kinds, expected) || !reflect.DeepEqual(observed, done.Usage) {
				t.Fatalf("events=%v; usage=%+v done=%+v", kinds, observed, done)
			}
			if len(kinds) > 1 && kinds[0] == proto.TypeUsage {
				original := usageFixture
				if mode == "native-error" {
					original = strings.ReplaceAll(strings.ReplaceAll(original, `"subtype":"success"`, `"subtype":"error_during_execution"`), `"is_error":false`, `"is_error":true`)
				}
				var want map[string]any
				_ = json.Unmarshal([]byte(original), &want)
				if !reflect.DeepEqual(observed.Raw["claude_sdk_result"], want) || observed.Model != "" || observed.Tokens != nil || observed.CostUSD != 0 || observed.InputTokens != 0 || observed.OutputTokens != 0 {
					t.Fatalf("usage normalized or lost: %+v", observed)
				}
			}
		})
	}
}

func runUsageHelper(request startRequest, mode string, encode func(bridgeEvent)) {
	mode = strings.TrimPrefix(mode, "usage-")
	value := json.RawMessage(usageFixture)
	if mode == "native-error" {
		value = []byte(strings.ReplaceAll(strings.ReplaceAll(string(value), `"subtype":"success"`, `"subtype":"error_during_execution"`), `"is_error":false`, `"is_error":true`))
	}
	id := request.Resume
	if mode == "wrong-session" {
		id = "other"
	}
	if mode == "malformed" {
		value = json.RawMessage(`[]`)
	}
	if mode != "missing" {
		encode(bridgeEvent{Type: "usage", SessionID: id, Usage: value})
	}
	if mode == "duplicate" {
		encode(bridgeEvent{Type: "usage", SessionID: id, Usage: value})
		return
	}
	if mode == "native-error" {
		encode(bridgeEvent{Type: "error", Code: "execution_failed"})
		return
	}
	if mode == "process-error" {
		os.Exit(7)
	}
	if mode == "changed-result" {
		id = "changed"
	}
	encode(bridgeEvent{Type: "result", SessionID: id, Text: "final"})
}

func verifyLiveUsageEvents(t *testing.T, events []proto.Envelope) {
	t.Helper()
	var observed proto.Usage
	count := 0
	for _, event := range events {
		switch event.Type {
		case proto.TypeUsage:
			var payload proto.UsagePayload
			if err := event.DecodePayload(&payload); err != nil {
				t.Fatal(err)
			}
			observed = payload.Usage
			count++
		case proto.TypeDone:
			var payload proto.DonePayload
			if err := event.DecodePayload(&payload); err != nil {
				t.Fatal(err)
			}
			if count != 1 || !reflect.DeepEqual(payload.Usage, observed) {
				t.Fatal("missing, repeated or changed live usage")
			}
		}
	}
	if count != 1 || observed.Tokens != nil || observed.Model != "" || observed.CostUSD != 0 {
		t.Fatal("live native evidence became unsupported public accounting")
	}
	snapshot, ok := observed.Raw["claude_sdk_result"].(map[string]any)
	if !ok || snapshot["subtype"] != "success" || snapshot["is_error"] != false {
		t.Fatal("missing native result provenance")
	}
	main, ok := snapshot["usage"].(map[string]any)
	if !ok {
		t.Fatal("missing main-loop usage")
	}
	output, ok := main["output_tokens"].(float64)
	if !ok || output <= 0 {
		t.Fatal("missing real output count")
	}
	models, ok := snapshot["modelUsage"].(map[string]any)
	if !ok || len(models) == 0 {
		t.Fatal("missing per-model native usage")
	}
}
