package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPublicMCPHTTPEffectiveConfiguration(t *testing.T) {
	for _, allowlist := range []*[]string{nil, new([]string), {"lookup", "query"}} {
		servers := map[string]mcpServerConfig{"docs": {URL: "https://docs.example/mcp", EnabledTools: allowlist}}
		for _, mutation := range []string{"none", "ambient", "url", "header", "header helper", "env header", "auth", "required", "tools", "disabled", "remote", "plugins", "apps", "keyring", "policy"} {
			t.Run(mutation+"/"+allowlistName(allowlist), func(t *testing.T) {
				response := mcpHTTPConfigResponse(servers)
				config := response["config"].(map[string]any)
				entries := config["mcp_servers"].(map[string]any)
				server := entries["docs"].(map[string]any)
				switch mutation {
				case "ambient":
					entries["operator"] = map[string]any{"command": "operator-mcp", "enabled": true}
				case "url":
					server["url"] = "https://other.example/mcp"
				case "header":
					server["http_headers"] = map[string]any{"Authorization": "synthetic-private"}
				case "header helper":
					server["http_headers_helper"] = "operator-credentials"
				case "env header":
					server["env_http_headers"] = map[string]any{"Authorization": "OPERATOR_SECRET"}
				case "auth":
					server["auth"] = "chatgpt"
				case "required":
					server["required"] = true
				case "tools":
					server["enabled_tools"] = []string{"undeclared"}
				case "disabled":
					server["enabled"] = false
				case "remote":
					server["environment_id"] = "remote"
				case "plugins", "apps":
					config["features"].(map[string]any)[mutation] = true
				case "keyring":
					config["mcp_oauth_credentials_store"] = "auto"
				case "policy":
					server["tools"] = map[string]any{"lookup": map[string]any{"enabled": false}}
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				if matchesMCPHTTPConfig(data, servers) != (mutation == "none") {
					t.Fatal("effective configuration decision differed", mutation)
				}
			})
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"config":{"mcp_servers":[]}}`, `not json`} {
		if matchesMCPHTTPConfig(json.RawMessage(raw), map[string]mcpServerConfig{}) {
			t.Fatal("missing or malformed native proof accepted")
		}
	}
	empty := map[string]mcpServerConfig{}
	data, _ := json.Marshal(mcpHTTPConfigResponse(empty))
	if !matchesMCPHTTPConfig(data, empty) {
		t.Fatal("explicit empty declaration rejected")
	}
}

func allowlistName(tools *[]string) string {
	if tools == nil {
		return "all"
	}
	if len(*tools) == 0 {
		return "none"
	}
	return "selected"
}

func TestPublicMCPHTTPRequiredConfigurationCannotBeWeakened(t *testing.T) {
	servers := map[string]mcpServerConfig{"docs": {URL: "https://docs.example/mcp", Required: true}}
	for _, value := range []any{true, false, nil, "true", "omitted"} {
		response := mcpHTTPConfigResponse(servers)
		server := response["config"].(map[string]any)["mcp_servers"].(map[string]any)["docs"].(map[string]any)
		server["required"] = value
		if value == "omitted" {
			delete(server, "required")
		}
		raw, err := json.Marshal(response)
		if err != nil || matchesMCPHTTPConfig(raw, servers) != (value == true) {
			t.Fatal("required initialization was weakened or rejected", value, err)
		}
	}
}

func TestPublicMCPHTTPPreflightRedactsNativeErrors(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	done := make(chan error, 1)
	go func() {
		done <- verifyMCPHTTPConfig(t.Context(), client.JSONRPCClient, SessionPlan{Cwd: "/private/workspace"})
	}()
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(server.FromClient).Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "config/read" || req.Params["cwd"] != "/private/workspace" || req.Params["includeLayers"] != false {
		t.Fatal("preflight did not request exact cwd configuration")
	}
	if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32603, "message": "synthetic-secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("native error was accepted or exposed", err)
	}
}

func TestPublicMCPHTTPPreparationChecksBeforeNewAndResumedThread(t *testing.T) {
	for _, mode := range []string{"new", "resume", "reject", "reject bearer reference", "remote new", "remote resume", "remote reject"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			remote := strings.HasPrefix(mode, "remote ")
			mode = strings.TrimPrefix(mode, "remote ")
			if !remote {
				req.RemoteEnvironment = nil
				req.DisableExecutionEnvironment = true
			}
			req.AgentOptions = map[string]any{"model": "fixture-model"}
			servers := []proto.MCPHTTPServer{{ServerLabel: "docs", ServerURL: "https://docs.example/mcp"}}
			if mode == "reject bearer reference" {
				token := "synthetic-private-bearer"
				servers[0].BearerToken = &token
			}
			req.MCPHTTPServers = &servers
			if mode == "resume" {
				req.AgentSessionID = "fixture-native-thread"
			}
			if !remote {
				t.Setenv("PARSAR_PREPARATION_STATUS", filepath.Join(root, "unknown-status"))
			}
			if err := os.WriteFile(filepath.Join(root, "unknown-status"), []byte("unknown"), 0o600); err != nil {
				t.Fatal(err)
			}
			declarations, err := publicMCPHTTPServers(req)
			if err != nil {
				t.Fatal(err)
			}
			response := mcpHTTPConfigResponse(declarations)
			if mode == "reject" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["operator"] = map[string]any{"url": "https://operator.example/private"}
			}
			if mode == "reject bearer reference" {
				response["config"].(map[string]any)["mcp_servers"].(map[string]any)["docs"].(map[string]any)["bearer_token_env_var"] = "OPERATOR_SECRET"
			}
			path := filepath.Join(root, "native-config.json")
			writeMCPHTTPConfigResponse(t, path, response)
			t.Setenv("PARSAR_PREPARATION_MCP_CONFIG", path)
			p, err := newPreparation(t.Context(), req, cfg)
			if strings.HasPrefix(mode, "reject") {
				if err == nil || p != nil {
					t.Fatal("ambient MCP configuration admitted")
				}
				assertPreparationOnly(t, root)
				waitPreparationMethod(t, root, "config/read")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			assertPreparationOnly(t, root)
			s, err := p.start(t.Context(), "actual-run", "actual prompt", make(chan proto.Envelope, 8))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			frames := waitPreparationMethod(t, root, "turn/start")
			checked, ready := false, !remote
			for _, frame := range frames {
				if frame.Method == "environment/info" {
					ready = true
				}
				if frame.Method == "config/read" {
					if !ready {
						t.Fatal("MCP check preceded remote readiness")
					}
					checked = true
				}
				if strings.HasPrefix(frame.Method, "thread/") {
					var params map[string]any
					if err := json.Unmarshal(frame.Params, &params); err != nil {
						t.Fatal(err)
					}
					if !checked || params["cwd"] != req.WorkDir || (frame.Method == "thread/resume") != (mode == "resume") {
						t.Fatal("thread started before the check or with another cwd")
					}
				}
				if frame.Method == "mcpServerStatus/list" {
					t.Fatal("preflight started discovery")
				}
			}
			_ = s.Cancel(context.Background())
			select {
			case <-s.waitDone:
			case <-time.After(4 * time.Second):
				t.Fatal("native fixture did not release")
			}
		})
	}
}
