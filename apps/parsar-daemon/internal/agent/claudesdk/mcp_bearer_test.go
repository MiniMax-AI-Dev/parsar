package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMCPBearerUsesFreshOwnedEnvironmentReferences(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
	tokens := []string{"first.synthetic+/==", "second-synthetic_token~"}
	tools := []string{"echo.v1"}
	servers := []proto.MCPHTTPServer{
		{ServerLabel: "first", ServerURL: "https://first.example/mcp", AllowedTools: &tools, BearerToken: &tokens[0]},
		{ServerLabel: "second", ServerURL: "https://second.example/mcp", BearerToken: &tokens[1]},
		{ServerLabel: "anonymous", ServerURL: "http://anonymous.example/mcp"},
	}
	req := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", DisableExecutionEnvironment: true, MCPHTTPServers: &servers, AgentOptions: map[string]any{"model": "fixture"}}
	seen := map[string]bool{}
	for range 2 {
		start, env, err := prepare(config, req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(start)
		if err != nil {
			t.Fatal(err)
		}
		for i, token := range tokens {
			reference := (*start.MCPHTTPServers)[i].BearerTokenEnvVar
			if !strings.HasPrefix(reference, "PARSAR_MCP_BEARER_") || seen[reference] || !slices.Contains(env, reference+"="+token) {
				t.Fatal("missing exact isolated credential or reused environment reference")
			}
			seen[reference] = true
			if _, exists := os.LookupEnv(reference); exists {
				t.Fatal("credential entered parent process environment")
			}
			if strings.Contains(string(raw), token) || !strings.Contains(string(raw), reference) {
				t.Fatal("bridge serialization contains a secret or omitted its reference")
			}
		}
		if (*start.MCPHTTPServers)[2].BearerTokenEnvVar != "" || strings.Contains(string(raw), `"bearer_token":`) {
			t.Fatal("anonymous declaration or bridge secret boundary changed")
		}
		tools[0] = "changed"
		if (*(*start.MCPHTTPServers)[0].AllowedTools)[0] != "echo.v1" {
			t.Fatal("tool selection was not copied")
		}
		tools[0] = "echo.v1"
		if servers[0].BearerToken != &tokens[0] || *servers[0].BearerToken != tokens[0] {
			t.Fatal("caller credential changed")
		}
	}
}

func TestMCPBearerRejectsInvalidCredentialBeforeStateCreation(t *testing.T) {
	for _, token := range []string{"", "=", " space", "space ", "has space", "line\r\ninjection", "nul\x00byte", "opaque中文", "middle=padding", "punctuation:invalid"} {
		root := t.TempDir()
		t.Setenv("PARSAR_HOME", root)
		config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
		servers := []proto.MCPHTTPServer{{ServerLabel: "fixture", ServerURL: "https://example.invalid/mcp", BearerToken: &token}}
		req := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", DisableExecutionEnvironment: true, MCPHTTPServers: &servers, AgentOptions: map[string]any{"model": "fixture"}}
		if _, _, err := prepare(config, req); err == nil || err.Error() != "claudesdk: unsupported HTTPS MCP bearer credential" {
			t.Fatal("invalid bearer accepted or unsafe error returned")
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid credential wrote execution state", err)
		}
	}
	for _, url := range []string{"http://example.invalid/mcp", "https://example.invalid/mcp#", "https://example.invalid/mcp?", "https://user:secret@example.invalid/mcp"} {
		token := "synthetic-token"
		servers := []proto.MCPHTTPServer{{ServerLabel: "fixture", ServerURL: url, BearerToken: &token}}
		if err := validateMCP(proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &servers}); err == nil {
			t.Fatal("unsafe authenticated endpoint accepted")
		}
	}
}
