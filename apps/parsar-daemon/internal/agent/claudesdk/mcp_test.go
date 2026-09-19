package claudesdk

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestHTTPMCPDeclaration(t *testing.T) {
	for _, mode := range []string{"unrestricted", "selected", "empty", "nil-slice", "required", "auth", "url-auth", "query", "wildcard", "reserved", "duplicate", "environment"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
			servers := []proto.MCPHTTPServer{{ServerLabel: "fixture", ServerURL: "https://example.invalid/mcp"}}
			req := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", DisableExecutionEnvironment: true, MCPHTTPServers: &servers, AgentOptions: map[string]any{"model": "fixture"}}
			tools := []string{"echo"}
			switch mode {
			case "selected":
				servers[0].AllowedTools = &tools
			case "empty":
				tools = []string{}
				servers[0].AllowedTools = &tools
			case "nil-slice":
				tools = nil
				servers[0].AllowedTools = &tools
			case "required":
				servers[0].Required = true
			case "auth":
				token := "private"
				servers[0].BearerToken = &token
			case "url-auth":
				servers[0].ServerURL = "https://user:secret@example.invalid/mcp"
			case "query":
				servers[0].ServerURL += "?"
			case "wildcard":
				tools = []string{"*"}
				servers[0].AllowedTools = &tools
			case "reserved":
				servers[0].ServerLabel = "functions"
			case "duplicate":
				servers = append(servers, servers[0])
			case "environment":
				req.DisableExecutionEnvironment = false
			}
			start, _, err := prepare(config, req)
			valid := mode == "unrestricted" || mode == "selected" || mode == "empty" || mode == "nil-slice" || mode == "auth" || mode == "required"
			if (err == nil) != valid {
				t.Fatalf("unexpected admission: %v", err)
			}
			if !valid {
				return
			}
			raw, _ := json.Marshal(start)
			if mode == "required" && !strings.Contains(string(raw), `"required":true`) {
				t.Fatal("required initialization was discarded")
			}
			if (mode == "empty" || mode == "nil-slice") && !strings.Contains(string(raw), `"allowed_tools":[]`) {
				t.Fatal("empty selection widened")
			}
			if mode == "selected" {
				tools[0] = "changed"
				if (*(*start.MCPHTTPServers)[0].AllowedTools)[0] != "echo" {
					t.Fatal("request did not snapshot tool selection")
				}
			}
		})
	}
}

func TestMCPObservationLifecycle(t *testing.T) {
	start := startRequest{MCPHTTPServers: &[]mcpHTTPServer{{ServerLabel: "fixture"}}, observeFunctions: true}
	state := mcpState{calls: map[string]proto.ToolObservation{}}
	var observations []proto.ToolCallPayload
	emit := func(kind string, payload any) {
		if kind != proto.TypeToolCall {
			t.Fatal(kind)
		}
		observations = append(observations, payload.(proto.ToolCallPayload))
	}
	before := bridgeEvent{Type: "mcp_observation", ID: "native", Stage: "before", Observation: &proto.ToolObservation{Kind: "mcp", Status: "in_progress", Name: "echo", Server: "fixture", Arguments: json.RawMessage(`{"value":7}`), Output: json.RawMessage(`null`), Error: json.RawMessage(`null`)}}
	if err := state.receive(before, start, emit); err != nil {
		t.Fatal(err)
	}
	if state.complete() {
		t.Fatal("unfinished call completed")
	}
	if err := state.receive(before, start, emit); err == nil {
		t.Fatal("duplicate call accepted")
	}
	after := before
	after.Stage = "after"
	n := *before.Observation
	after.Observation = &n
	n.Status = "completed"
	n.Output = json.RawMessage(`{"content":"value","structuredContent":{"number":7}}`)
	if err := state.receive(after, start, emit); err != nil {
		t.Fatal(err)
	}
	if !state.complete() || string(observations[1].Observation.Output) != string(n.Output) {
		t.Fatal("native result changed")
	}
	before.ID = "unfinished"
	if err := state.receive(before, start, emit); err != nil {
		t.Fatal(err)
	}
	state.close(start, emit)
	if !state.complete() || observations[len(observations)-1].Observation.Status != "incomplete" {
		t.Fatal("cancellation lost pending call")
	}
	if err := state.receive(after, start, emit); err == nil {
		t.Fatal("terminal call reopened")
	}
}
