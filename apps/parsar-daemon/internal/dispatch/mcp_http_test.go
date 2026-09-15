package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMCPHTTPBearerRejectsUnsupportedRequestsBeforeFactory(t *testing.T) {
	for _, mode := range []string{"supported", "no bearer capability", "no MCP capability", "unavailable", "no none capability", "local", "remote", "other engine", "HTTP", "credential-free", "product"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			token := "synthetic-private-token"
			servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
			req := proto.PromptRequestPayload{AgentKind: "codex", DisableExecutionEnvironment: true, MCPHTTPServers: &servers}
			caps := proto.AgentKindCapabilities{EnvironmentNone: true, MCPHTTPTools: true, MCPHTTPBearerAuth: true}
			switch mode {
			case "no bearer capability", "credential-free", "product":
				caps.MCPHTTPBearerAuth = false
			case "no MCP capability":
				caps.MCPHTTPTools = false
			case "no none capability":
				caps.EnvironmentNone = false
			case "local":
				req.DisableExecutionEnvironment = false
			case "remote":
				req.DisableExecutionEnvironment = false
				req.RemoteEnvironment = &proto.RemoteEnvironment{ID: "remote"}
				caps.RemoteEnvironment = true
			case "other engine":
				req.AgentKind = "claude_sdk"
			case "HTTP":
				servers[0].ServerURL = "http://tools.example/mcp"
			}
			if mode == "credential-free" {
				servers[0].BearerToken = nil
			}
			if mode == "product" {
				req.MCPHTTPServers, req.DisableExecutionEnvironment = nil, false
			}
			called := false
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: req.AgentKind, Available: mode != "unavailable", Capabilities: caps},
				func(_ context.Context, got proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
					called = true
					if mode == "supported" && (got.MCPHTTPServers == nil || (*got.MCPHTTPServers)[0].BearerToken == nil || *(*got.MCPHTTPServers)[0].BearerToken != token) {
						t.Error("token lost before adapter")
					}
					return nil, errors.New("controlled factory stop")
				})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "mcp-bearer", req))
			if called != (mode == "supported" || mode == "credential-free" || mode == "product") {
				t.Fatal("wrong factory admission")
			}
			frames := h.sender.snapshot()
			raw, _ := json.Marshal(frames)
			if err == nil || strings.Contains(err.Error(), token) || strings.Contains(string(raw), token) || len(frames) != 2 || frames[0].Type != proto.TypeError || frames[1].Type != proto.TypeDone {
				t.Fatal("terminal rejection missing or exposed credential")
			}
		})
	}
}
