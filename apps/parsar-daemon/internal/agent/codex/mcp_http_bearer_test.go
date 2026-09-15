package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMCPHTTPBearerPlanSeparatesServersAndProcesses(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "none", true: "remote"}[remote], func(t *testing.T) {
			t.Setenv("PARSAR_HOME", t.TempDir())
			tokens := []string{"first-synthetic.token+/==", "second-synthetic_token~"}
			servers := []proto.MCPHTTPServer{
				{ServerLabel: "first", ServerURL: "https://first.example/mcp", BearerToken: &tokens[0]},
				{ServerLabel: "second", ServerURL: "https://second.example/mcp", BearerToken: &tokens[1]},
				{ServerLabel: "public", ServerURL: "http://public.example/mcp"},
			}
			req := proto.PromptRequestPayload{AgentStateKey: "retained-mcp", DisableExecutionEnvironment: true, MCPHTTPServers: &servers}
			if remote {
				req = remoteEnvironmentRequest()
				req.MCPHTTPServers = &servers
			}
			seen := map[string]bool{}
			for range 2 {
				plan, _, err := prepareSessionPlan(t.Context(), req, defaultSessionConfig())
				if err != nil {
					t.Fatal(err)
				}
				defer plan.Cleanup()
				config, err := os.ReadFile(filepath.Join(plan.Cwd, "config.toml"))
				if err != nil {
					t.Fatal(err)
				}
				args, _ := json.Marshal(plan.ExtraConfig)
				for i, server := range servers[:2] {
					ref := plan.mcpHTTPServers[server.ServerLabel].BearerTokenEnvVar
					if !strings.HasPrefix(ref, "PARSAR_MCP_BEARER_") || seen[ref] || !slices.Contains(plan.Env, ref+"="+tokens[i]) {
						t.Fatal("missing exact per-server secret or reused native reference")
					}
					seen[ref] = true
					if _, present := os.LookupEnv(ref); present {
						t.Fatal("secret entered parent environment")
					}
					if !strings.Contains(string(config), `bearer_token_env_var = "`+ref+`"`) || strings.Contains(string(config), tokens[i]) || strings.Contains(string(args), tokens[i]) {
						t.Fatal("secret reached configuration/arguments or reference was omitted")
					}
				}
				if plan.mcpHTTPServers["public"].BearerTokenEnvVar != "" || strings.Count(string(config), "bearer_token_env_var") != 2 {
					t.Fatal("credential-free server received authentication")
				}
				plan.Cleanup()
			}
		})
	}
}

func TestMCPHTTPBearerRejectsInvalidTokensWithoutPersistence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	for _, token := range []string{"", "=", " has-space", "has-space ", "has space", "line\r\ninjection", "nul\x00byte", "opaque中文", "middle=padding", "punctuation:invalid"} {
		servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
		req := proto.PromptRequestPayload{AgentStateKey: "invalid-bearer", DisableExecutionEnvironment: true, MCPHTTPServers: &servers}
		if _, _, err := prepareSessionPlan(t.Context(), req, defaultSessionConfig()); err == nil || err.Error() != "codex: unsupported HTTPS MCP bearer credential" {
			t.Fatal("invalid bearer value accepted or unsafe error returned")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid credential wrote native state", err)
	}
}

func TestMCPHTTPBearerDoesNotReachModelCatalogProbe(t *testing.T) {
	if !SupportsTextVerbosity {
		t.Skip("catalog probe requires Unix")
	}
	t.Setenv("PARSAR_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "catalog-probe")
	script := "#!/bin/sh\nif env | grep -q '^PARSAR_MCP_BEARER_'; then exit 9; fi\nprintf '%s' '{\"models\":[{\"slug\":\"fixture-model\",\"support_verbosity\":true}]}'\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	token := "synthetic-catalog-secret"
	servers := []proto.MCPHTTPServer{{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}}
	req := proto.PromptRequestPayload{AgentStateKey: "catalog", DisableExecutionEnvironment: true, MCPHTTPServers: &servers,
		AgentOptions: map[string]any{"model": "fixture-model", "model_verbosity": "medium"}}
	cfg := defaultSessionConfig()
	cfg.codexBinary = binary
	plan, _, err := prepareSessionPlan(t.Context(), req, cfg)
	if err != nil {
		t.Fatal("catalog probe inherited bearer or failed", err)
	}
	defer plan.Cleanup()
	if !slices.Contains(plan.Env, plan.mcpHTTPServers["tools"].BearerTokenEnvVar+"="+token) {
		t.Fatal("app-server did not receive bearer after catalog probe")
	}
}

func TestMCPHTTPBearerPreflightMatchesOnlyItsServerReference(t *testing.T) {
	servers := map[string]mcpServerConfig{
		"first":  {URL: "https://first.example/mcp", BearerTokenEnvVar: "PARSAR_MCP_BEARER_FIRST"},
		"second": {URL: "https://second.example/mcp", BearerTokenEnvVar: "PARSAR_MCP_BEARER_SECOND"},
		"public": {URL: "http://public.example/mcp"},
	}
	for _, mutation := range []string{"none", "missing", "ambient", "swapped", "extra", "header", "helper"} {
		t.Run(mutation, func(t *testing.T) {
			response := mcpHTTPConfigResponse(servers)
			entries := response["config"].(map[string]any)["mcp_servers"].(map[string]any)
			first := entries["first"].(map[string]any)
			switch mutation {
			case "missing":
				delete(first, "bearer_token_env_var")
			case "ambient":
				first["bearer_token_env_var"] = "OPERATOR_SECRET"
			case "swapped":
				first["bearer_token_env_var"] = servers["second"].BearerTokenEnvVar
			case "extra":
				entries["public"].(map[string]any)["bearer_token_env_var"] = servers["first"].BearerTokenEnvVar
			case "header":
				first["http_headers"] = map[string]string{"Authorization": "Bearer synthetic-private"}
			case "helper":
				first["http_headers_helper"] = "operator-helper"
			}
			raw, err := json.Marshal(response)
			if err != nil || matchesMCPHTTPConfig(raw, servers) != (mutation == "none") {
				t.Fatal("incorrect authenticated configuration decision", err)
			}
		})
	}
}
