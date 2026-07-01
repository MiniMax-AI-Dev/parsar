package agentdaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

var errStubBuiltinLookup = errors.New("stub: builtin flag lookup failed")

// fixedSigner mints a deterministic token so tests can assert the connector
// threads the signer output into the MCP entry's env verbatim.
func fixedSigner(conversationID string) string { return "tok-" + conversationID }

func enabledConnector() *Connector {
	return &Connector{
		imHistoryEndpoint: "https://parsar.example/internal/im/history",
		imHistoryToken:    fixedSigner,
		log:               discardLogger(),
	}
}

// TestIMHistoryMCPServer_BuildsEntry: a configured connector produces a `sh -c`
// entry carrying the endpoint, minted token, and conversation id in env.
func TestIMHistoryMCPServer_BuildsEntry(t *testing.T) {
	c := enabledConnector()
	name, entry, ok := c.imHistoryMCPServer("conv-1")
	if !ok {
		t.Fatal("expected tool to be enabled")
	}
	if name != imHistoryServerName {
		t.Fatalf("server name = %q, want %q", name, imHistoryServerName)
	}
	if entry["command"] != "sh" {
		t.Fatalf("command = %v, want sh", entry["command"])
	}
	args, _ := entry["args"].([]string)
	if len(args) != 2 || args[0] != "-c" || args[1] != imHistoryMCPScript {
		t.Fatalf("args = %#v", entry["args"])
	}
	env, _ := entry["env"].(map[string]string)
	if env["PARSAR_IM_HISTORY_URL"] != "https://parsar.example/internal/im/history" {
		t.Fatalf("url env = %q", env["PARSAR_IM_HISTORY_URL"])
	}
	if env["PARSAR_IM_HISTORY_TOKEN"] != "tok-conv-1" {
		t.Fatalf("token env = %q", env["PARSAR_IM_HISTORY_TOKEN"])
	}
	if env["PARSAR_CONVERSATION_ID"] != "conv-1" {
		t.Fatalf("conv env = %q", env["PARSAR_CONVERSATION_ID"])
	}
}

// TestIMHistoryMCPServer_Disabled: any missing precondition disables the tool
// so no half-wired entry (with an empty token or unroutable URL) ships.
func TestIMHistoryMCPServer_Disabled(t *testing.T) {
	cases := map[string]*Connector{
		"no endpoint":     {imHistoryToken: fixedSigner, log: discardLogger()},
		"no signer":       {imHistoryEndpoint: "https://x/y", log: discardLogger()},
		"empty token out": {imHistoryEndpoint: "https://x/y", imHistoryToken: func(string) string { return "" }, log: discardLogger()},
	}
	for name, c := range cases {
		if _, _, ok := c.imHistoryMCPServer("conv-1"); ok {
			t.Fatalf("%s: expected disabled", name)
		}
	}
	// Empty conversation id also disables (nothing to scope the token to).
	if _, _, ok := enabledConnector().imHistoryMCPServer(""); ok {
		t.Fatal("empty conversation id must disable")
	}
}

// TestResolveCapabilityAdditions_InjectsHistoryTool: the tool is mounted even
// when the agent has zero other capabilities (capabilities store nil, which
// short-circuits the enumeration) — proving auto-mount is independent of any
// configured capability.
func TestResolveCapabilityAdditions_InjectsHistoryTool(t *testing.T) {
	c := enabledConnector() // capabilities store is nil
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}
	got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	entry, ok := got.MCPServers[imHistoryServerName]
	if !ok {
		t.Fatalf("history tool not injected: %#v", got.MCPServers)
	}
	if _, isMap := entry.(map[string]any); !isMap {
		t.Fatalf("entry type = %T", entry)
	}
}

// TestResolveCapabilityAdditions_SkipsNonClaudeTarget: other agent kinds
// consume MCP config with a different schema, so the MVP injection is gated to
// the Claude Code render target.
func TestResolveCapabilityAdditions_SkipsNonClaudeTarget(t *testing.T) {
	c := enabledConnector()
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}
	got, err := c.resolveCapabilityAdditions(context.Background(), in, "codex")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if _, ok := got.MCPServers[imHistoryServerName]; ok {
		t.Fatal("history tool must not inject for non-claude target")
	}
}

// TestResolveCapabilityAdditions_HistoryToolGatedByBuiltinFlag: when the
// per-agent built-in flag reports OFF, the runtime injection is suppressed so
// the agent can no longer call the tool. A store returning ON (or no row) keeps
// it mounted.
func TestResolveCapabilityAdditions_HistoryToolGatedByBuiltinFlag(t *testing.T) {
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}

	t.Run("disabled suppresses injection", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{
			builtinDisabled: map[string]bool{imHistoryServerName: true},
		}
		got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
		if err != nil {
			t.Fatalf("resolveCapabilityAdditions: %v", err)
		}
		if _, ok := got.MCPServers[imHistoryServerName]; ok {
			t.Fatalf("history tool must be suppressed when built-in flag is OFF: %#v", got.MCPServers)
		}
	})

	t.Run("enabled keeps injection", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{} // no disabled entries => default ON
		got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
		if err != nil {
			t.Fatalf("resolveCapabilityAdditions: %v", err)
		}
		if _, ok := got.MCPServers[imHistoryServerName]; !ok {
			t.Fatalf("history tool must inject when built-in flag is ON: %#v", got.MCPServers)
		}
	})

	t.Run("flag lookup error defaults to injecting", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{builtinErr: errStubBuiltinLookup}
		got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
		if err != nil {
			t.Fatalf("resolveCapabilityAdditions: %v", err)
		}
		if _, ok := got.MCPServers[imHistoryServerName]; !ok {
			t.Fatalf("history tool must inject when the flag lookup fails (never block on bookkeeping): %#v", got.MCPServers)
		}
	})
}
