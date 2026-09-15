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
	for _, missing := range []string{"durable_input_receipts", "execution_controls", "function_tools", "tool_observations", "mcp_http_tools"} {
		for _, prebound := range []bool{false, true} {
			t.Run(missing+"/"+map[bool]string{false: "select", true: "bound"}[prebound], func(t *testing.T) {
				h := newFunctionHarness(t)
				configuration := functionConfiguration
				if missing == "mcp_http_tools" {
					configuration = mcpWorkerConfiguration
				}
				if !prebound || missing == "mcp_http_tools" {
					var err error
					h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "unbound", Configuration: []byte(configuration)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if prebound && missing == "mcp_http_tools" {
					if err := h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
						t.Fatal(err)
					}
				}
				caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: missing != "durable_input_receipts", EnvironmentNone: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: missing != "execution_controls", SubagentControl: true, ToolObservations: missing != "tool_observations", MCPHTTPTools: missing != "mcp_http_tools", FunctionTools: missing != "function_tools" && missing != "mcp_http_tools"}
				heartbeat := func() {
					h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
				}
				heartbeat()
				deadline := time.Now().Add(3 * time.Second)
				for {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, _, _ := peer.AgentKindStatus("codex")
					if info.Capabilities.ExecutionControls == caps.ExecutionControls && info.Capabilities.FunctionTools == caps.FunctionTools && info.Capabilities.ToolObservations == caps.ToolObservations && info.Capabilities.MCPHTTPTools == caps.MCPHTTPTools {
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
				caps.DurableInputReceipts, caps.ExecutionControls, caps.ToolObservations, caps.MCPHTTPTools = true, true, true, true
				caps.FunctionTools = missing != "mcp_http_tools"
				heartbeat()
				request := h.read(proto.TypePromptRequest)
				var prompt proto.PromptRequestPayload
				if request.DecodePayload(&prompt) != nil {
					t.Fatal("invalid prompt")
				}
				if missing == "mcp_http_tools" {
					if prompt.MCPHTTPServers == nil || len(*prompt.MCPHTTPServers) != 1 || (*prompt.MCPHTTPServers)[0].ServerLabel != "tickets" || (*prompt.MCPHTTPServers)[0].AllowedTools == nil || len(*(*prompt.MCPHTTPServers)[0].AllowedTools) != 0 || len(prompt.FunctionTools) != 0 {
						t.Fatal("MCP declaration lost during dispatch", prompt)
					}
				} else if len(prompt.FunctionTools) != 1 || prompt.FunctionTools[0].Name != "lookup_ticket" {
					t.Fatal(prompt)
				}
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
				waitTurn(t, h, input.TurnID, store.TurnCompleted)
			})
		}
	}
}

const mcpWorkerConfiguration = `{"agent":{"model":"gpt-5.5","tools":[{"type":"mcp","server_label":"tickets","transport":{"type":"http","server_url":"http://127.0.0.1:9191/mcp"},"connection_origin":"service","allowed_tools":[],"credential_id":null,"request_metadata":{},"required":false}]},"environment":{"type":"none"}}`
