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
	for _, mode := range []string{"supported", "claude", "claude old peer", "claude local", "claude remote", "no bearer capability", "no MCP capability", "unavailable", "no none capability", "local", "remote", "other engine", "HTTP", "credential-free", "product", "required", "required old peer", "optional old peer"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			token := "synthetic-private-token"
			servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
			req := proto.PromptRequestPayload{AgentKind: "codex", DisableExecutionEnvironment: true, MCPHTTPServers: &servers}
			caps := proto.AgentKindCapabilities{EnvironmentNone: true, MCPHTTPTools: true, MCPHTTPBearerAuth: true}
			switch mode {
			case "claude", "claude old peer", "claude local", "claude remote":
				req.AgentKind = "claude_sdk"
				caps.MCPHTTPBearerAuth = mode != "claude old peer"
				if mode == "claude local" || mode == "claude remote" {
					req.DisableExecutionEnvironment = false
				}
				if mode == "claude remote" {
					req.RemoteEnvironment = &proto.RemoteEnvironment{ID: "remote"}
					caps.RemoteEnvironment, caps.MCPHTTPRemoteEnvironment, caps.MCPHTTPRemoteBearerAuth = true, true, true
				}
			case "required", "required old peer", "optional old peer":
				servers[0].Required = mode != "optional old peer"
				servers[0].BearerToken = nil
				caps.MCPHTTPRequired = mode == "required"
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
				req.AgentKind = "other"
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
					if mode == "required" && !(*got.MCPHTTPServers)[0].Required {
						t.Error("required initialization lost before adapter")
					}
					if (mode == "supported" || mode == "claude") && (got.MCPHTTPServers == nil || (*got.MCPHTTPServers)[0].BearerToken == nil || *(*got.MCPHTTPServers)[0].BearerToken != token) {
						t.Error("token lost before adapter")
					}
					return nil, errors.New("controlled factory stop")
				})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "mcp-bearer", req))
			if called != (mode == "supported" || mode == "claude" || mode == "claude remote" || mode == "other engine" || mode == "credential-free" || mode == "product" || mode == "required" || mode == "optional old peer") {
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
	for _, mode := range []string{"supported", "old peer", "no MCP", "no remote", "bearer old peer", "bearer supported", "bearer no general auth", "bearer none conflict", "bearer HTTP", "other engine", "empty declaration", "no declaration", "required", "required old peer"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp"}}
			req := preparationRequest()
			req.Configuration.AgentKind = "codex"
			req.Configuration.MCPHTTPServers = &servers
			caps := proto.AgentKindCapabilities{RemoteEnvironment: true, MCPHTTPTools: true, MCPHTTPRemoteEnvironment: true, EnvironmentNone: true, MCPHTTPBearerAuth: true}
			switch mode {
			case "required", "required old peer":
				servers[0].Required = true
				caps.MCPHTTPRequired = mode == "required"
			case "old peer":
				caps.MCPHTTPRemoteEnvironment = false
			case "no MCP":
				caps.MCPHTTPTools = false
			case "no remote":
				caps.RemoteEnvironment = false
			case "other engine":
				req.Configuration.AgentKind = "other"
			case "bearer old peer", "bearer supported", "bearer no general auth", "bearer none conflict", "bearer HTTP":
				token := "synthetic-private-token"
				servers[0].BearerToken = &token
				caps.MCPHTTPRemoteBearerAuth = mode != "bearer old peer"
				caps.EnvironmentNone = false
				if mode == "bearer no general auth" {
					caps.MCPHTTPBearerAuth = false
				}
				if mode == "bearer none conflict" {
					req.Configuration.DisableExecutionEnvironment = true
				}
				if mode == "bearer HTTP" {
					servers[0].ServerURL = "http://tools.example/mcp"
				}
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
			h.reg.RegisterPreparation(req.Configuration.AgentKind, false, func(_ context.Context, got proto.PromptRequestPayload) (agent.Prepared, error) {
				if mode == "required" && !(*got.MCPHTTPServers)[0].Required {
					t.Error("required initialization lost before preparation")
				}
				if mode == "bearer supported" && (got.MCPHTTPServers == nil || (*got.MCPHTTPServers)[0].BearerToken == nil || *(*got.MCPHTTPServers)[0].BearerToken != "synthetic-private-token") {
					t.Error("remote bearer lost before preparation")
				}
				entered <- struct{}{}
				return nil, errors.New("controlled stop")
			})
			err := h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "remote-mcp", req))
			allowed := mode == "supported" || mode == "other engine" || mode == "no declaration" || mode == "bearer supported" || mode == "required"
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
