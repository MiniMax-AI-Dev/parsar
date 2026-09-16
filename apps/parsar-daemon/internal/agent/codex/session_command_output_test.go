package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestCommandOutputRequiresOptInAndRootTurn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		out := make(chan proto.Envelope, 10)
		s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: defaultSessionConfig(), rpc: NewJSONRPCClient(JSONRPCConfig{}), observeToolObservations: enabled}
		s.registerHandlers()
		s.setThreadID("root")
		s.beginRootTurn("root", "turn")
		for _, raw := range []string{
			`{"threadId":"child","turnId":"turn","itemId":"cmd","delta":"foreign"}`,
			`{"threadId":"root","turnId":"old","itemId":"cmd","delta":"foreign"}`,
			`{"threadId":"root","turnId":"turn","delta":"missing identity"}`,
			`{"threadId":"root","turnId":"turn","itemId":"cmd","delta":null}`,
			`{"threadId":42}`, `{}`,
		} {
			scopeNotification(t, s, "item/commandExecution/outputDelta", raw)
		}
		if len(out) != 0 {
			t.Fatal("invalid output reached root")
		}
		for _, fragment := range []string{"same\n", "same\n", "结束\n"} {
			raw, _ := json.Marshal(map[string]string{"threadId": "root", "turnId": "turn", "itemId": "cmd", "delta": fragment})
			scopeNotification(t, s, "item/commandExecution/outputDelta", string(raw))
			if !enabled {
				if len(out) != 0 {
					t.Fatal("product frame sequence changed")
				}
				continue
			}
			if len(out) != 1 {
				t.Fatal("missing registered command notification")
			}
			env := <-out
			var p proto.CommandOutputPayload
			if env.DecodePayload(&p) != nil || env.Type != proto.TypeCommandOutput || env.ID != "run" || p.ID != "cmd" || p.Delta != fragment {
				t.Fatal("command fragment changed", env)
			}
		}
	}
}
