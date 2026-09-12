package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func toolSnapshotFixtures() []string {
	return []string{
		`{"type":"commandExecution","id":"cmd","command":"printf result","cwd":"/tmp","status":"completed","aggregatedOutput":"result\n","exitCode":0,"durationMs":37}`,
		`{"type":"commandExecution","id":"fail","command":"exit 2","status":"failed","aggregatedOutput":"failure details","exitCode":2,"durationMs":12}`,
		`{"type":"mcpToolCall","id":"mcp","server":"reference","tool":"lookup","arguments":{"id":"record"},"status":"completed","result":{"content":[{"type":"text","text":"answer"}],"structuredContent":{"version":9007199254740993}},"error":null}`,
		`{"type":"mcpToolCall","id":"mcp-error","server":"reference","tool":"lookup","arguments":{},"status":"failed","result":null,"error":{"message":"lookup failed"}}`,
		`{"type":"dynamicToolCall","id":"dynamic","tool":"lookup","namespace":"reference","arguments":["a","b"],"status":"completed","contentItems":[{"type":"inputText","text":"dynamic output"}],"success":true}`,
		`{"type":"fileChange","id":"edit","status":"completed","changes":[{"path":"/tmp/example","kind":{"type":"update","move_path":null},"diff":"-before\n+after"}]}`,
		`{"type":"webSearch","id":"search","query":"reference","action":{"type":"search","query":"reference","queries":["reference"]}}`,
	}
}

func TestToolSnapshotsPreserveNativeResultsOnlyWhenRequested(t *testing.T) {
	for _, item := range toolSnapshotFixtures() {
		var identity struct{ ID string }
		if err := json.Unmarshal([]byte(item), &identity); err != nil {
			t.Fatal(err)
		}
		t.Run(identity.ID, func(t *testing.T) {
			var legacy []proto.Envelope
			for _, enabled := range []bool{false, true} {
				out := make(chan proto.Envelope, 4)
				s := &Session{runID: "run", observeTools: enabled, out: out, cancelCtx: context.Background(), bufs: NewItemBuffers(), cfg: defaultSessionConfig()}
				raw := json.RawMessage(`{"threadId":"private-thread","turnId":"private-turn","item":` + item + `}`)
				s.onItemStarted(raw)
				s.onItemCompleted(raw)
				if len(out) != 2 {
					t.Fatalf("tool event count changed: %d", len(out))
				}
				for i, stage := range []string{"before", "after"} {
					event := <-out
					var tool proto.ToolCallPayload
					if err := event.DecodePayload(&tool); err != nil {
						t.Fatal(err)
					}
					if event.Type != proto.TypeToolCall || event.ID != "run" || tool.ID != identity.ID || tool.Stage != stage {
						t.Fatalf("tool identity/stage changed: %+v %+v", event, tool)
					}
					if !enabled {
						if tool.NativeItem != nil || tool.Observation != nil || bytes.Contains(event.Payload, []byte("native_item")) {
							t.Fatal("legacy request acquired tool snapshot")
						}
						legacy = append(legacy, event)
						continue
					}
					var expected bytes.Buffer
					if err := json.Compact(&expected, []byte(item)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(tool.NativeItem, expected.Bytes()) {
						t.Fatalf("native result lost or coerced: %s", tool.NativeItem)
					}
					tool.NativeItem = nil
					payload, err := json.Marshal(tool)
					if err != nil || !bytes.Equal(payload, legacy[i].Payload) {
						t.Fatalf("legacy tool fields changed: %s, %v", payload, err)
					}
				}
			}
		})
	}
}

func TestToolObservationsReplaceNativeSnapshotsWhenRequested(t *testing.T) {
	for _, nativeSnapshots := range []bool{false, true} {
		for _, item := range toolSnapshotFixtures() {
			var source struct{ ID string }
			if err := json.Unmarshal([]byte(item), &source); err != nil {
				t.Fatal(err)
			}
			t.Run(source.ID, func(t *testing.T) {
				out := make(chan proto.Envelope, 4)
				s := &Session{runID: "run", observeTools: nativeSnapshots, observeToolObservations: true, out: out, cancelCtx: context.Background(), bufs: NewItemBuffers(), cfg: defaultSessionConfig()}
				raw := json.RawMessage(`{"item":` + item + `}`)
				s.onItemStarted(raw)
				s.onItemCompleted(raw)
				if len(out) != 2 {
					t.Fatalf("tool event count changed: %d", len(out))
				}
				for _, stage := range []string{"before", "after"} {
					event := <-out
					var tool proto.ToolCallPayload
					if event.DecodePayload(&tool) != nil || event.Type != proto.TypeToolCall || tool.ID != source.ID || tool.Stage != stage || tool.Observation == nil {
						t.Fatal(event)
					}
					if tool.NativeItem != nil || bytes.Contains(event.Payload, []byte("native_item")) {
						t.Fatal("duplicate native snapshot")
					}
					if stage == "before" && tool.Observation.Status != "in_progress" {
						t.Fatal(tool.Observation)
					}
					if source.ID == "mcp" && !bytes.Contains(tool.Observation.Output, []byte("9007199254740993")) {
						t.Fatal("structured output precision lost")
					}
				}
			})
		}
	}
}
