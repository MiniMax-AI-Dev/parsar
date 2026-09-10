package opencode_test

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSessionUsageCarriesSelectedModel(t *testing.T) {
	for _, tc := range []struct {
		name, selector, fallback, want string
	}{
		{"managed selector wins", "anthropic/MiniMax-M3", "old-model", "MiniMax-M3"},
		{"model path", "openrouter/anthropic/claude", "", "anthropic/claude"},
		{"legacy model", "", "openai/gpt-4o", "gpt-4o"},
		{"bare model", "", "MiniMax-M3", "MiniMax-M3"},
		{"native default remains unknown", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := opencodeHelperReq("run_model", "hello", "json-success")
			req.AgentOptions["model_selector"] = tc.selector
			req.AgentOptions["model"] = tc.fallback
			out := make(chan proto.Envelope, 32)
			session, err := opencode.NewSessionForTest(context.Background(), req, out, opencodeHelperConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel(context.Background())
			events, closed := drainOpenCode(t, out, 5*time.Second)
			if !closed {
				t.Fatal("session did not finish")
			}
			count := 0
			for _, event := range events {
				var usage proto.Usage
				switch event.Type {
				case proto.TypeUsage:
					usage = decodePayload[proto.UsagePayload](t, event).Usage
				case proto.TypeDone:
					usage = decodePayload[proto.DonePayload](t, event).Usage
				default:
					continue
				}
				count++
				if usage.Model != tc.want || usage.Provider != "opencode" || usage.InputTokens != 4 || usage.OutputTokens != 2 {
					t.Fatalf("%s usage = %#v; want model %q with existing provider/tokens", event.Type, usage, tc.want)
				}
			}
			if count != 2 {
				t.Fatalf("usage and done frames = %d, want 2", count)
			}
		})
	}
}
