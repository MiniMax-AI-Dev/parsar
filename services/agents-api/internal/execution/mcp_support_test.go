package execution

import (
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func mcpSupportFixture(t *testing.T) (Snapshot, []proto.MCPHTTPServer, device.KindCapabilities) {
	t.Helper()
	vault, credential := uuid.NewString(), uuid.NewString()
	tool := json.RawMessage(`{"type":"mcp","server_label":"tickets","connection_origin":"service","transport":{"type":"http","server_url":"https://mcp.example/tools"}}`)
	snapshot := Snapshot{Agent: v1.Agent{Model: "model", Tools: []json.RawMessage{tool}}, Environment: &v1.Environment{Type: "none"}, VaultIDs: []string{vault},
		MCPCredentials: []store.MCPCredentialBinding{{ServerLabel: "tickets", ServerURL: "https://mcp.example/tools", VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}}}
	_, servers, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		t.Fatal(err)
	}
	caps := device.KindCapabilities{EnvironmentNone: true, MCPHTTPTools: true, MCPHTTPBearerAuth: true, MCPHTTPRequired: true,
		Preparation: true, RemoteEnvironment: true, MCPHTTPRemoteEnvironment: true, MCPHTTPRemoteBearerAuth: true}
	return snapshot, servers, caps
}

func TestMCPPublicBearerPolicyIsIndependentOfRuntimeCapabilities(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "unknown"} {
		t.Run(engine, func(t *testing.T) {
			snapshot, servers, caps := mcpSupportFixture(t)
			raw, _ := json.Marshal(snapshot)
			allowed := engine == "codex" || engine == "claude_sdk"
			if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != allowed {
				t.Fatal("creation bypassed public credential policy", err)
			}
			if (Policy{}).canAdmitInputs(engine, raw) != allowed {
				t.Fatal("later input bypassed public credential policy")
			}
			if _, err := (Policy{}).mcpExecutionCredentials(engine, snapshot, servers, caps); (err == nil) != allowed {
				t.Fatal("runtime capabilities widened public admission", err)
			}
			request, err := (&Dispatcher{}).executionRequest(t.Context(), store.Session{Engine: engine}, snapshot, caps, store.SessionExecutionBinding{})
			if err == nil || request.MCPHTTPServers != nil {
				t.Fatal("credential execution without a store was admitted")
			}
			if allowed && err.Error() != "authenticated MCP execution is unavailable" {
				t.Fatal("accepted profile did not reach scoped credential lookup", err)
			}
			if !allowed && err.Error() != "The configured engine currently supports anonymous HTTP MCP only." {
				t.Fatal("unverified profile bypassed public policy", err)
			}
		})
	}
}

func TestMCPExecutionChecksRequireVerifiedCapabilityCombinations(t *testing.T) {
	for _, placement := range []string{"none", "self_hosted"} {
		for _, missing := range []string{"", "mcp", "bearer", "placement", "required", "preparation", "remote-mcp", "remote-bearer", "daemon", "environment"} {
			t.Run(placement+"/"+missing, func(t *testing.T) {
				snapshot, servers, caps := mcpSupportFixture(t)
				snapshot.Environment.Type = placement
				snapshot.Environment.WorkspaceDirectory = "/work"
				servers[0].Required = true
				var tool v1.MCPTool
				if err := json.Unmarshal(snapshot.Agent.Tools[0], &tool); err != nil {
					t.Fatal(err)
				}
				tool.Required = true
				snapshot.Agent.Tools[0], _ = json.Marshal(tool)
				switch missing {
				case "mcp":
					caps.MCPHTTPTools = false
				case "bearer":
					caps.MCPHTTPBearerAuth = false
				case "placement":
					caps.EnvironmentNone, caps.RemoteEnvironment = false, false
				case "required":
					caps.MCPHTTPRequired = false
				case "preparation":
					caps.Preparation = false
				case "remote-mcp":
					caps.MCPHTTPRemoteEnvironment = false
				case "remote-bearer":
					caps.MCPHTTPRemoteBearerAuth = false
				case "daemon":
					snapshot.Daemon = &DaemonConfig{WorkDir: "/work"}
				case "environment":
					snapshot.Environment = nil
				}
				allowed := missing == "" || placement == "none" && (missing == "preparation" || missing == "remote-mcp" || missing == "remote-bearer")
				selected, err := (Policy{}).mcpExecutionCredentials("codex", snapshot, servers, caps)
				if (err == nil) != allowed || allowed && len(selected) != 1 {
					t.Fatal("incorrect combined MCP capability decision", err)
				}
				if !allowed {
					request, requestErr := (&Dispatcher{}).executionRequest(t.Context(), store.Session{Engine: "codex"}, snapshot, caps, store.SessionExecutionBinding{})
					if requestErr == nil || request.MCPHTTPServers != nil || requestErr.Error() == "authenticated MCP execution is unavailable" {
						t.Fatal("request bypassed capability checks before credential lookup", requestErr)
					}
				}
			})
		}
	}
}

func TestMCPAnonymousExecutionPreservesFrozenDecision(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk"} {
		for _, mode := range []string{"unattached", "frozen anonymous", "invalid binding"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				snapshot, _, caps := mcpSupportFixture(t)
				caps.MCPHTTPBearerAuth = false
				if mode == "unattached" {
					snapshot.VaultIDs, snapshot.MCPCredentials = nil, nil
				} else {
					snapshot.MCPCredentials[0].VaultID = ""
					snapshot.MCPCredentials[0].CredentialID = ""
					snapshot.MCPCredentials[0].AuthType = ""
					if mode == "invalid binding" {
						snapshot.MCPCredentials[0].ServerURL += "/different"
					}
				}
				raw, _ := json.Marshal(snapshot)
				allowed := mode != "invalid binding"
				if err := (Policy{}).ValidateSessionConfiguration(engine, raw); (err == nil) != allowed || (Policy{}).canAdmitInputs(engine, raw) != allowed {
					t.Fatal("anonymous binding validation changed", err)
				}
				request, err := (&Dispatcher{}).executionRequest(t.Context(), store.Session{Engine: engine}, snapshot, caps, store.SessionExecutionBinding{})
				if !allowed {
					if err == nil || request.MCPHTTPServers != nil {
						t.Fatal("invalid frozen binding reached dispatch")
					}
					return
				}
				if err != nil || request.MCPHTTPServers == nil || len(*request.MCPHTTPServers) != 1 || (*request.MCPHTTPServers)[0].BearerToken != nil {
					t.Fatal("anonymous dispatch gained a credential requirement", err)
				}
			})
		}
	}
}
