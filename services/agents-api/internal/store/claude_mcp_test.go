package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestClaudeMCPWaitsForCapableRuntime(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		for _, prebound := range []bool{false, true} {
			t.Run(fmt.Sprintf("bearer=%t/bound=%t", authenticated, prebound), func(t *testing.T) {
				h := newFunctionHarness(t)
				configuration, token := mcpWorkerConfiguration, ""
				if authenticated {
					configuration, token = mcpBearerWorkerConfiguration(t, h)
				}
				claudeSession(t, h, configuration, prebound)
				// An older bundle may support anonymous MCP but not bearer authentication.
				caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, ExecutionControls: true, EnvironmentNone: true, SubagentControl: true, ToolObservations: true, MCPHTTPTools: authenticated}
				h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "claude_sdk", Available: true, Capabilities: caps}}})
				awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "Claude MCP heartbeat", func() bool {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, found, known := peer.AgentKindStatus("claude_sdk")
					return known && found && info.Capabilities.MCPHTTPTools == authenticated && !info.Capabilities.MCPHTTPBearerAuth
				})
				input := h.message("start", "Use declared tools only")
				if prebound {
					if result := <-h.run(t.Context(), input.TurnID); result.err == nil {
						t.Fatal("final preclaim accepted a runtime without MCP support")
					}
				}
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
				turn, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
				if err != nil || turn.Status != store.TurnQueued {
					t.Fatal("incapable runtime claimed work", turn, err)
				}
				if !prebound {
					if _, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
						t.Fatal("bound an incapable runtime", err)
					}
				}
				caps.MCPHTTPTools, caps.MCPHTTPBearerAuth = true, authenticated
				h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "claude_sdk", Available: true, Capabilities: caps}}})
				var prompt proto.PromptRequestPayload
				if h.read(proto.TypePromptRequest).DecodePayload(&prompt) != nil || prompt.AgentKind != "claude_sdk" || prompt.MCPHTTPServers == nil || len(*prompt.MCPHTTPServers) != 1 {
					t.Fatal("missing typed MCP dispatch")
				}
				server := (*prompt.MCPHTTPServers)[0]
				if server.ServerLabel != "tickets" || server.AllowedTools == nil || len(*server.AllowedTools) != 0 || server.Required || !prompt.DisableExecutionEnvironment || !prompt.DisableSubagents {
					t.Fatal("MCP configuration changed during dispatch")
				}
				endpoint := "http://127.0.0.1:9191/mcp"
				if authenticated {
					endpoint = "https://mcp.example/tools"
				}
				if server.ServerURL != endpoint || (token != "" && (server.BearerToken == nil || *server.BearerToken != token)) || (token == "" && server.BearerToken != nil) {
					t.Fatal("dispatch lost scoped authentication or authenticated an anonymous server")
				}
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
				waitTurn(t, h, input.TurnID, store.TurnCompleted)
			})
		}
	}
}

func TestClaudeMCPUnsupportedSnapshotRejectedBeforeClaim(t *testing.T) {
	for _, profile := range []string{"required", "reserved label"} {
		t.Run(profile, func(t *testing.T) {
			h := newDispatchHarness(t)
			configuration := mcpWorkerConfiguration
			switch profile {
			case "required":
				configuration = strings.Replace(configuration, `"required":false`, `"required":true`, 1)
			case "reserved label":
				configuration = strings.Replace(configuration, `"tickets"`, `"functions"`, 1)
			}
			claudeSession(t, h, configuration, true)
			caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, ExecutionControls: true, EnvironmentNone: true, SubagentControl: true, ToolObservations: true, MCPHTTPTools: true, MCPHTTPRequired: true, MCPHTTPBearerAuth: true}
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "claude_sdk", Available: true, Capabilities: caps}}})
			deadline := time.Now().Add(3 * time.Second)
			for {
				peer, _ := h.registry.LookupDevice(h.device.ID)
				if info, _, _ := peer.AgentKindStatus("claude_sdk"); info.Capabilities.MCPHTTPBearerAuth {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("heartbeat missing")
				}
				time.Sleep(10 * time.Millisecond)
			}
			input := h.message("start", "Run")
			if result := <-h.run(t.Context(), input.TurnID); result.err == nil {
				t.Fatal("unsupported MCP configuration claimed")
			}
			turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, input.TurnID)
			if err != nil || turn.Status != store.TurnQueued {
				t.Fatal(turn, err)
			}
		})
	}
}
