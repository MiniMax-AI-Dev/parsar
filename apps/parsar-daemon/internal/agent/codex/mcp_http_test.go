package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPublicMCPHTTPPlanOwnsConfigurationAndPreservesHistory(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	tools := []string{"lookup.docs", `quote"tool`}
	denyAll := []string{}
	servers := []proto.MCPHTTPServer{
		{ServerLabel: "docs.server", ServerURL: "https://docs.example/mcp", AllowedTools: &tools},
		{ServerLabel: "blocked", ServerURL: "http://127.0.0.1:12345/mcp", AllowedTools: &denyAll},
	}
	original := map[string]any{
		"mcp_servers":     map[string]any{"operator": map[string]any{"command": "operator-mcp"}},
		"enable_features": []any{"apps", "plugins", "unrelated"},
	}
	req := proto.PromptRequestPayload{AgentStateKey: "public-mcp", DisableExecutionEnvironment: true, MCPHTTPServers: &servers, AgentOptions: original}
	req.AgentOptions = executionOptions(req)
	plan, _, err := prepareSessionPlan(t.Context(), req, defaultSessionConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if original["mcp_servers"] == nil || req.AgentOptions["mcp_servers"] != nil {
		t.Fatal("declaration failed to replace operator MCP without mutation")
	}
	if !slices.Equal(plan.EnableFeatures, []string{"unrelated"}) || !slices.Contains(plan.DisableFeatures, "apps") || !slices.Contains(plan.DisableFeatures, "plugins") || !slices.Contains(plan.ExtraConfig, [2]string{"mcp_oauth_credentials_store", `"file"`}) {
		t.Fatal("native profile was not pinned")
	}
	home, err := allocCodexHome(req.AgentStateKey)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cwd != home {
		t.Fatal("empty cwd did not resolve to the private home")
	}
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`[mcp_servers."docs.server"]`, `enabled_tools = ["lookup.docs", "quote\"tool"]`, `enabled_tools = []`} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("missing native config %q", expected)
		}
	}
	if strings.Contains(string(config), "operator") {
		t.Fatal("operator MCP was rendered")
	}
	tools[0] = "mutated"
	if (*plan.mcpHTTPServers["docs.server"].EnabledTools)[0] != "lookup.docs" {
		t.Fatal("prepared allowlist retained caller-owned memory")
	}
	history := filepath.Join(home, "retained-history.jsonl")
	if err := os.WriteFile(history, []byte("native-history"), 0o600); err != nil {
		t.Fatal(err)
	}
	servers = []proto.MCPHTTPServer{{ServerLabel: "replacement", ServerURL: "https://new.example/mcp"}}
	second, _, err := prepareSessionPlan(t.Context(), req, defaultSessionConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cleanup()
	config, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || strings.Contains(string(config), "docs.server") || !strings.Contains(string(config), "replacement") || strings.Contains(string(config), "enabled_tools") {
		t.Fatal("cold configuration retained old servers or changed unrestricted tools", err)
	}
	retained, err := os.ReadFile(history)
	if err != nil || string(retained) != "native-history" {
		t.Fatal("configuration reset changed native history", err)
	}
}

func TestPublicMCPHTTPRejectsInvalidProfileAndStoredCredentials(t *testing.T) {
	valid := []proto.MCPHTTPServer{{ServerLabel: "docs", ServerURL: "https://docs.example/mcp"}}
	for _, req := range []proto.PromptRequestPayload{
		{MCPHTTPServers: &valid},
		{MCPHTTPServers: &valid, DisableExecutionEnvironment: true, RemoteEnvironment: &proto.RemoteEnvironment{ID: "remote"}},
	} {
		if _, err := publicMCPHTTPServers(req); err == nil {
			t.Fatal("non-service profile accepted")
		}
	}
	for _, server := range []proto.MCPHTTPServer{
		{ServerLabel: "codex_apps", ServerURL: "https://docs.example/mcp"},
		{ServerLabel: "docs", ServerURL: "https://user:synthetic-secret@docs.example/mcp"},
		{ServerLabel: "docs", ServerURL: "https://docs.example/mcp?token=synthetic-secret"},
		{ServerLabel: "docs", ServerURL: "file:///tmp/mcp"},
	} {
		servers := []proto.MCPHTTPServer{server}
		if _, err := publicMCPHTTPServers(proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &servers}); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("unsupported configuration was accepted or exposed", err)
		}
	}
	t.Setenv("PARSAR_HOME", t.TempDir())
	home, err := allocCodexHome("credentials")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".credentials.json")
	stored := []byte(`{"synthetic":"private"}`)
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	req := proto.PromptRequestPayload{AgentStateKey: "credentials", DisableExecutionEnvironment: true, MCPHTTPServers: &valid}
	if _, _, err := prepareSessionPlan(t.Context(), req, defaultSessionConfig()); err == nil {
		t.Fatal("existing MCP credentials accepted")
	}
	if after, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(after, stored) {
		t.Fatal("existing credentials were modified", err)
	}
}

// This is the pinned native serializer's config/read shape, not live discovery.
func mcpHTTPConfigResponse(servers map[string]mcpServerConfig) map[string]any {
	entries := make(map[string]any, len(servers))
	for name, server := range servers {
		entry := map[string]any{"url": server.URL, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil}
		if server.EnabledTools != nil {
			entry["enabled_tools"] = append([]string{}, (*server.EnabledTools)...)
		}
		if server.BearerTokenEnvVar != "" {
			entry["bearer_token_env_var"] = server.BearerTokenEnvVar
		}
		entries[name] = entry
	}
	return map[string]any{"config": map[string]any{"mcp_servers": entries, "features": map[string]any{"plugins": false, "apps": false}, "mcp_oauth_credentials_store": "file"}}
}

func writeMCPHTTPConfigResponse(t *testing.T, path string, response any) {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteMCPRejectsBearerBeforeNativeSetup(t *testing.T) {
	req := remoteEnvironmentRequest()
	token := "synthetic-private-token"
	servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
	req.MCPHTTPServers = &servers
	if _, err := publicMCPHTTPServers(req); err == nil || strings.Contains(err.Error(), token) {
		t.Fatal("remote bearer accepted or exposed")
	}
	servers[0].BearerToken = nil
	if _, err := publicMCPHTTPServers(req); err != nil {
		t.Fatal("credential-free remote declaration rejected", err)
	}
}
