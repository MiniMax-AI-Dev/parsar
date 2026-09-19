package mcode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// A successful public real-model run supplies an actual foreign native ID.
// Neither that history nor a missing ID may silently become a new session.
func TestNativeMCodeHistoryIsolation(t *testing.T) {
	binary, options, foreign := os.Getenv("PARSAR_MCODE_BIN"), os.Getenv("PARSAR_MCODE_REAL_OPTIONS"), os.Getenv("PARSAR_MCODE_FOREIGN_NATIVE_ID")
	if binary == "" || options == "" || foreign == "" {
		t.Skip("native executable, private provider options and foreign history ID required")
	}
	raw, err := os.ReadFile(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"foreign": foreign, "missing": "00000000-0000-4000-8000-000000000000"} {
		t.Run(name, func(t *testing.T) {
			req := executionRequest(t)
			if json.Unmarshal(raw, &req.AgentOptions) != nil {
				t.Fatal("invalid private options")
			}
			req.AgentSessionID, req.Prompt = id, "This input must never execute."
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 64)
			session, err := newSession(ctx, req, out, binary)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := session.Cancel(cleanup); err != nil {
					t.Error(err)
				}
			}()
			rejected := false
			for event := range out {
				if event.Type == proto.TypeError {
					var failure proto.ErrorPayload
					_ = json.Unmarshal(event.Payload, &failure)
					rejected = strings.Contains(failure.Error, "session/load:") && !strings.Contains(failure.Error, "deadline exceeded")
				}
				if event.Type == proto.TypeDone {
					var done proto.DonePayload
					_ = json.Unmarshal(event.Payload, &done)
					if done.Content != "" || done.Metadata[proto.DoneMetaAgentSessionID] != nil {
						t.Fatal("unowned history executed or silently replaced")
					}
				}
			}
			if !rejected {
				t.Fatal("native history was not explicitly rejected")
			}
		})
	}
}
