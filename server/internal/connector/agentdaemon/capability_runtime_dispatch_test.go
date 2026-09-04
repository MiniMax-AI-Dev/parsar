package agentdaemon

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/render"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestAgentKindToRenderTarget pins the mapping between agent_kind strings
// (as written by resolveAgentKind from agent config) and
// the render target the capability serializer should produce.
//
// Unknown / empty kinds must fall back to TargetClaudeCode so legacy rows
// keep rendering as before.
func TestTargetForAgentKind(t *testing.T) {
	cases := []struct {
		agentKind string
		want      render.Target
	}{
		{"claude_code", render.TargetClaudeCode},
		{"opencode", render.TargetOpenCode},
		{"codex", render.TargetCodex},
		{"pi", render.TargetPi},
		{"", render.TargetClaudeCode},
		{"  claude_code  ", render.TargetClaudeCode},
		{"unknown_engine", render.TargetClaudeCode},
	}
	for _, tc := range cases {
		t.Run(tc.agentKind, func(t *testing.T) {
			if got := render.TargetForAgentKind(tc.agentKind); got != tc.want {
				t.Fatalf("render.TargetForAgentKind(%q) = %q, want %q", tc.agentKind, got, tc.want)
			}
		})
	}
}

func TestResolveCapabilityAdditions_ManagedSkillsRender(t *testing.T) {
	for _, agentKind := range []string{"codex", "opencode"} {
		t.Run(agentKind, func(t *testing.T) {
			c := &Connector{
				capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
					newSkillRow(t, "skill-a", "Skill A", "do a"),
				}},
				oss: &stubPluginPresigner{},
				log: discardLogger(),
			}
			got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), agentKind)
			if err != nil {
				t.Fatalf("resolve skill: %v", err)
			}
			if len(got.Skills) != 1 {
				t.Fatalf("expected 1 skill, got %+v", got.Skills)
			}
			if len(got.Disabled) != 0 {
				t.Fatalf("supported skill must not be disabled, got %+v", got.Disabled)
			}
		})
	}
}

// TestResolveCapabilityAdditions_OpenCodePluginSoftDegrades mirrors the
// Codex case for the opencode engine. opencode's renderer returns
// ErrUnsupported for KindPlugin; without soft-degrade, every prompt
// against an opencode agent with a plugin enabled would 500.
func TestResolveCapabilityAdditions_OpenCodePluginSoftDegrades(t *testing.T) {
	presigner := &stubPluginPresigner{}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newPluginRow(t, "p1", "my-plugin", "capabilities/plugins/u1/my-plugin.zip", validSHA256),
		}},
		oss: presigner,
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "opencode")
	if err != nil {
		t.Fatalf("opencode plugin must soft-degrade, got hard error: %v", err)
	}
	if len(got.Plugins) != 0 {
		t.Fatalf("opencode must not produce plugins, got %+v", got.Plugins)
	}
	if len(got.Disabled) != 1 || got.Disabled[0].CapabilityID != "p1" {
		t.Fatalf("expected 1 disabled plugin capability with id=p1, got %+v", got.Disabled)
	}
}

// TestResolveCapabilityAdditions_OpenCodeMCPStillRenders confirms an
// engine that supports a Kind must produce output, not a Disabled entry —
// the soft-degrade path only kicks in for unsupported Kinds. The codex
// MCP equivalent is covered by TestResolveCapabilityAdditions_CodexMCPStillRenders.
func TestResolveCapabilityAdditions_OpenCodeMCPStillRenders(t *testing.T) {
	row := newMCPRow(t, "mcp-1", "github", []canonical.MCPServer{
		{Name: "github", Command: "npx", Args: []string{"@modelcontextprotocol/server-github"}},
	}, nil)
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "opencode")
	if err != nil {
		t.Fatalf("opencode mcp must render: %v", err)
	}
	if len(got.MCPServers) != 1 {
		t.Fatalf("expected 1 mcp server, got %d (%+v)", len(got.MCPServers), got.MCPServers)
	}
	if _, ok := got.MCPServers["github"]; !ok {
		t.Fatalf("github mcp server missing from output: %+v", got.MCPServers)
	}
	if len(got.Disabled) != 0 {
		t.Fatalf("opencode mcp must not be disabled, got %+v", got.Disabled)
	}
}

// TestResolveCapabilityAdditions_PiSkillRenders is the positive half of
// pi's scope: pi delivers managed skills (via --skill), so a skill row on
// a pi agent must render, not degrade.
func TestResolveCapabilityAdditions_PiSkillRenders(t *testing.T) {
	presigner := &stubPluginPresigner{}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newSkillRow(t, "skill-a", "Skill A", "do a"),
		}},
		oss: presigner,
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "pi")
	if err != nil {
		t.Fatalf("pi skill must render: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("pi must render skills, got %d (%+v)", len(got.Skills), got.Skills)
	}
	if len(got.Disabled) != 0 {
		t.Fatalf("pi skill must not be disabled, got %+v", got.Disabled)
	}
}

// TestResolveCapabilityAdditions_PiMCPSoftDegrades is the negative half:
// managed MCP is out of scope for pi, so the pi renderer returns
// ErrUnsupported and the connector must skip the row as a Disabled
// capability rather than hard-fail. This depends on agentKindToRenderTarget
// mapping "pi"→TargetPi; were pi to fall back to TargetClaudeCode it would
// wrongly render the MCP server instead of degrading.
func TestResolveCapabilityAdditions_PiMCPSoftDegrades(t *testing.T) {
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newMCPRow(t, "mcp-1", "github", []canonical.MCPServer{
				{Name: "github", Command: "npx", Args: []string{"@modelcontextprotocol/server-github"}},
			}, nil),
		}},
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "pi")
	if err != nil {
		t.Fatalf("pi mcp must soft-degrade, got hard error: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Fatalf("pi must not produce mcp servers, got %+v", got.MCPServers)
	}
	if len(got.Disabled) != 1 || got.Disabled[0].CapabilityID != "mcp-1" {
		t.Fatalf("expected 1 disabled mcp capability with id=mcp-1, got %+v", got.Disabled)
	}
}

// TestResolveCapabilityAdditions_EmptyAgentKindDefaultsClaudeCode pins
// the back-compat default: an agent persisted before the
// agent_kind column existed (or one whose config omits it) is treated
// as claude_code. Otherwise an upgrade would silently disable every
// skill/plugin on legacy agents.
func TestResolveCapabilityAdditions_EmptyAgentKindDefaultsClaudeCode(t *testing.T) {
	presigner := &stubPluginPresigner{}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newSkillRow(t, "skill-a", "Skill A", "do a"),
		}},
		oss: presigner,
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "")
	if err != nil {
		t.Fatalf("empty agent_kind must default to claude_code: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("legacy default must still render skills, got %d", len(got.Skills))
	}
	if len(got.Disabled) != 0 {
		t.Fatalf("legacy default must not produce disabled entries, got %+v", got.Disabled)
	}
}

func TestBuildAgentOptions_CodexSkillIsEnabled(t *testing.T) {
	sm := &fakeSystemMessageStore{}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newSkillRow(t, "skill-a", "Skill A", "do a"),
		}},
		oss:            &stubPluginPresigner{},
		systemMessages: sm,
		log:            discardLogger(),
	}

	in := connector.PromptInput{
		RunID:                   "run-1",
		ConversationID:          "conv-1",
		WorkspaceID:             "ws-1",
		AgentID:                 "agt-1",
		ConversationInitiatorID: "user-1",
		AgentConfig:             map[string]any{"agent_kind": "codex"},
	}

	opts, err := c.buildAgentOptions(context.Background(), in)
	if err != nil {
		t.Fatalf("buildAgentOptions: %v", err)
	}
	if _, ok := opts["skills"]; !ok {
		t.Fatalf("opts['skills'] missing for codex: %+v", opts)
	}
	if len(sm.runtimeErrors) != 0 {
		t.Fatalf("supported skill must not emit runtime_error notices: %+v", sm.runtimeErrors)
	}
}

// TestBuildAgentOptions_OpenCodeMCPDoesNotNotice is the negative control
// for the previous test: an engine that supports the capability kind
// must NOT surface a disabled-capability notice. Otherwise a misfiring
// dispatch would spam every channel with bogus credential nudges for
// MCP servers that loaded fine.
func TestBuildAgentOptions_OpenCodeMCPDoesNotNotice(t *testing.T) {
	sm := &fakeSystemMessageStore{}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newMCPRow(t, "mcp-1", "github", []canonical.MCPServer{
				{Name: "github", Command: "npx", Args: []string{"@modelcontextprotocol/server-github"}},
			}, nil),
		}},
		systemMessages: sm,
		log:            discardLogger(),
	}

	in := connector.PromptInput{
		RunID:                   "run-1",
		ConversationID:          "conv-1",
		WorkspaceID:             "ws-1",
		AgentID:                 "agt-1",
		ConversationInitiatorID: "user-1",
		AgentConfig:             map[string]any{"agent_kind": "opencode"},
	}

	opts, err := c.buildAgentOptions(context.Background(), in)
	if err != nil {
		t.Fatalf("buildAgentOptions: %v", err)
	}
	servers, _ := opts["mcp_servers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Fatalf("github mcp must appear in opts['mcp_servers'], got %+v", opts["mcp_servers"])
	}
	if len(sm.runtimeErrors) != 0 {
		t.Fatalf("supported capability must not surface a disabled notice, got %+v", sm.runtimeErrors)
	}
}
