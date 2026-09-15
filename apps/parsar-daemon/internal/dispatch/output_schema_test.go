package dispatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestOutputSchemaAdmissionBeforeFactoryOrPreparation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"supported", "omitted", "old peer", "unavailable", "null", "array"} {
			t.Run(mode+map[bool]string{true: "/prepared", false: "/prompt"}[prepared], func(t *testing.T) {
				h := newHarness(t)
				defer h.router.Shutdown(context.Background())
				req := preparationRequest().Configuration
				req.OutputSchema = json.RawMessage(`{"const":9007199254740993}`)
				caps := proto.AgentKindCapabilities{RemoteEnvironment: true, OutputSchema: mode != "old peer"}
				switch mode {
				case "omitted":
					req.OutputSchema, caps.OutputSchema = nil, false
				case "null":
					req.OutputSchema = json.RawMessage(`null`)
				case "array":
					req.OutputSchema = json.RawMessage(`[]`)
				}
				var calls atomic.Int32
				check := func(got proto.PromptRequestPayload) {
					calls.Add(1)
					if !bytes.Equal(got.OutputSchema, req.OutputSchema) {
						t.Error("schema changed before native adapter")
					}
				}
				h.reg.RegisterKind(proto.SupportedAgentKind{Kind: req.AgentKind, Available: mode != "unavailable", Capabilities: caps}, func(_ context.Context, got proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
					check(got)
					return nil, errors.New("controlled factory failure")
				})
				h.reg.RegisterPreparation(req.AgentKind, func(_ context.Context, got proto.PromptRequestPayload) (agent.Prepared, error) {
					check(got)
					return nil, errors.New("controlled preparation failure")
				})
				accepted := mode == "supported" || mode == "omitted"
				if prepared {
					_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "schema-prepare", proto.ExecutionPreparePayload{Configuration: req}))
					state := "rejected"
					if accepted {
						state = "failed"
					}
					waitPreparationStatus(t, h.sender, "schema-prepare", state, "")
				} else {
					if err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "schema-run", req)); err == nil {
						t.Fatal("expected admission or controlled factory failure")
					}
				}
				if (calls.Load() == 1) != accepted {
					t.Fatal("unsupported schema reached an execution factory", calls.Load())
				}
			})
		}
	}
}
