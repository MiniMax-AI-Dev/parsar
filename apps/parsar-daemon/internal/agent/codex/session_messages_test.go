package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMessageObservationIsOptInAndKeepsNativeBoundaries(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "observed"}[enabled], func(t *testing.T) {
			out := make(chan proto.Envelope, 16)
			s := &Session{runID: "run", observeMessages: enabled, out: out, cancelCtx: context.Background(), bufs: NewItemBuffers(), cfg: defaultSessionConfig()}
			s.onItemStarted(json.RawMessage(`{"item":{"type":"agentMessage","id":"a","phase":"commentary"}}`))
			s.onAgentDelta(json.RawMessage(`{"itemId":"a","delta":"first"}`))
			s.onItemStarted(json.RawMessage(`{"item":{"type":"agentMessage","id":"b","phase":"final_answer"}}`))
			s.onAgentDelta(json.RawMessage(`{"itemId":"b","delta":"second"}`))
			s.onItemCompleted(json.RawMessage(`{"item":{"type":"agentMessage","id":"a","phase":"commentary","text":"first complete"}}`))
			// b stays unfinished, as when the native request is cancelled before item/completed.
			s.onItemCompleted(json.RawMessage(`{"item":{"type":"agentMessage","id":"c","phase":"final_answer","text":"without deltas"}}`))
			var deltas []proto.DeltaPayload
			var messages []proto.OutputMessagePayload
			for len(out) > 0 {
				env := <-out
				switch env.Type {
				case proto.TypeDelta:
					var p proto.DeltaPayload
					if err := env.DecodePayload(&p); err != nil {
						t.Fatal(err)
					}
					deltas = append(deltas, p)
				case proto.TypeOutputMessage:
					var p proto.OutputMessagePayload
					if err := env.DecodePayload(&p); err != nil {
						t.Fatal(err)
					}
					messages = append(messages, p)
				default:
					t.Fatalf("unexpected frame: %s", env.Type)
				}
			}
			if len(deltas) != 2 || deltas[0].Delta != "first" || deltas[1].Delta != "second" || deltas[1].Sequence != 2 {
				t.Fatalf("legacy text changed: %+v", deltas)
			}
			if !enabled {
				if len(messages) != 0 || deltas[0].ItemID != "" || deltas[1].ItemID != "" {
					t.Fatal("legacy request gained observations")
				}
				return
			}
			if deltas[0].ItemID != "a" || deltas[1].ItemID != "b" || len(messages) != 4 {
				t.Fatalf("identity lost: %+v %+v", deltas, messages)
			}
			if messages[0].ID != "a" || messages[0].Phase != "commentary" || messages[0].Status != "in_progress" || messages[0].Text != nil {
				t.Fatalf("start: %+v", messages[0])
			}
			if messages[1].ID != "b" || messages[1].Phase != "final_answer" || messages[1].Status != "in_progress" {
				t.Fatalf("second start: %+v", messages[1])
			}
			if messages[2].ID != "a" || messages[2].Status != "completed" || messages[2].Text == nil || *messages[2].Text != "first complete" {
				t.Fatalf("completion: %+v", messages[2])
			}
			if messages[3].ID != "c" || messages[3].Text == nil || *messages[3].Text != "without deltas" {
				t.Fatalf("non-streamed message lost: %+v", messages[3])
			}
		})
	}
}
