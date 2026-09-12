package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestWorkerWaitsForToolCapabilities(t *testing.T) {
	for _, missing := range []string{"function_tools", "tool_observations"} {
		for _, prebound := range []bool{false, true} {
			t.Run(missing+"/"+map[bool]string{false: "select", true: "bound"}[prebound], func(t *testing.T) {
				h := newFunctionHarness(t)
				if !prebound {
					var err error
					h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "unbound", Configuration: []byte(functionConfiguration)})
					if err != nil {
						t.Fatal(err)
					}
				}
				caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, EnvironmentNone: true, WebSearchControl: true, TextVerbosity: true, SubagentControl: true, ToolObservations: missing != "tool_observations", FunctionTools: missing != "function_tools"}
				heartbeat := func() {
					h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
				}
				heartbeat()
				deadline := time.Now().Add(3 * time.Second)
				for {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, _, _ := peer.AgentKindStatus("codex")
					if info.Capabilities.FunctionTools == caps.FunctionTools && info.Capabilities.ToolObservations == caps.ToolObservations {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("heartbeat not applied")
					}
					time.Sleep(10 * time.Millisecond)
				}
				input := h.message("queued", "Look up ticket")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				worker, err := execution.StartWorker(ctx, h.d)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- worker.Run(ctx) }()
				defer func() {
					cancel()
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("worker did not stop")
					}
				}()
				time.Sleep(650 * time.Millisecond)
				current, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
				if err != nil || current.Status != store.TurnQueued {
					t.Fatal(current, err)
				}
				if !prebound {
					if _, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
						t.Fatal("bound an incapable device", err)
					}
				}
				caps.FunctionTools, caps.ToolObservations = true, true
				heartbeat()
				request := h.read(proto.TypePromptRequest)
				var prompt proto.PromptRequestPayload
				if request.DecodePayload(&prompt) != nil || len(prompt.FunctionTools) != 1 || prompt.FunctionTools[0].Name != "lookup_ticket" {
					t.Fatal(prompt)
				}
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
				waitTurn(t, h, input.TurnID, store.TurnCompleted)
			})
		}
	}
}
