package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestRemoteMCPRejectsBeforePreparationFactory(t *testing.T) {
	for _, mode := range []string{"supported", "old peer", "no MCP", "no remote", "bearer", "other engine", "empty declaration", "no declaration"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp"}}
			req := preparationRequest()
			req.Configuration.AgentKind = "codex"
			req.Configuration.MCPHTTPServers = &servers
			caps := proto.AgentKindCapabilities{RemoteEnvironment: true, MCPHTTPTools: true, MCPHTTPRemoteEnvironment: true, EnvironmentNone: true, MCPHTTPBearerAuth: true}
			switch mode {
			case "old peer":
				caps.MCPHTTPRemoteEnvironment = false
			case "no MCP":
				caps.MCPHTTPTools = false
			case "no remote":
				caps.RemoteEnvironment = false
			case "other engine":
				req.Configuration.AgentKind = "other"
			case "bearer":
				token := "synthetic-private-token"
				servers[0].BearerToken = &token
			case "empty declaration":
				servers = []proto.MCPHTTPServer{}
				caps.MCPHTTPRemoteEnvironment = false
			case "no declaration":
				req.Configuration.MCPHTTPServers = nil
				caps.MCPHTTPRemoteEnvironment = false
			}
			entered := make(chan struct{}, 1)
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: req.Configuration.AgentKind, Available: true, Capabilities: caps}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
				t.Error("ordinary factory called")
				return nil, errors.New("unexpected")
			})
			h.reg.RegisterPreparation(req.Configuration.AgentKind, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
				entered <- struct{}{}
				return nil, errors.New("controlled stop")
			})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "remote-mcp", req))
			allowed := mode == "supported" || mode == "no declaration"
			if (err == nil) != allowed {
				t.Fatal("wrong preparation admission", err)
			}
			if allowed {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("factory not called")
				}
				waitPreparationStatus(t, h.sender, "remote-mcp", "failed", "")
			} else {
				select {
				case <-entered:
					t.Fatal("rejected request reached factory")
				default:
				}
			}
		})
	}
}
