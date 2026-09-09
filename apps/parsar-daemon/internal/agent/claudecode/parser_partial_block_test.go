package claudecode_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTranslatePerBlockAssistantPreservesStreamWithoutCopies(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_blocks", nil, counterMinter())
	frames := []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Checking."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Checking."}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"PARSAR"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"-IM-OK"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"PARSAR-IM-OK"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"path":"policy.md"}}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"PARSAR-IM-OK"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"PARSAR-IM-OK"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":3}}`,
		`{"type":"result","subtype":"success","result":"PARSAR-IM-OK"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"complete-only"}]}}`,
	}
	var text, thinking string
	var tools, done int
	for _, frame := range frames {
		out, err := tr.Translate([]byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		for _, env := range out.Envelopes {
			switch env.Type {
			case proto.TypeDelta:
				text += mustDecode[proto.DeltaPayload](t, env.Payload).Delta
			case proto.TypeThinking:
				thinking += mustDecode[proto.ThinkingPayload](t, env.Payload).Text
			case proto.TypeToolCall:
				tools++
			case proto.TypeDone:
				done++
				if got := mustDecode[proto.DonePayload](t, env.Payload).Content; got != "PARSAR-IM-OK" {
					t.Fatalf("final content = %q", got)
				}
			}
		}
	}
	if text != "PARSAR-IM-OKPARSAR-IM-OKcomplete-only" || thinking != "Checking." || tools != 1 || done != 1 {
		t.Fatalf("text=%q thinking=%q tools=%d done=%d", text, thinking, tools, done)
	}
}
