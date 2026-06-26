package render

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// codexRenderer serializes capability specs for the Codex agent runtime
// (`codex app-server --stdio`). Today only KindMCP is supported; Codex
// has its own Agent Skills convention that is not interchangeable with
// Claude Code's, and Codex has no plugin concept at all — both return
// ErrUnsupported, which the agentdaemon connector treats as a soft
// degrade (skip + surface a disabled-capability notice).
//
// The KindMCP wire shape is intentionally identical to
// claudeCodeRenderer's (`{"mcpServers": {"<name>": {"command","args","env"}}}`)
// so the connector's claudeCodeMCPDocument unmarshal in
// capability_runtime.go works against either target without a per-engine
// branch. Codex itself consumes MCP servers via a TOML file under its
// CODEX_HOME (`[mcp_servers.<name>]`), but that TOML conversion happens
// in the daemon at session-spawn time — by then the renderer's pure
// JSON has already been merged into agent_options.mcp_servers.
type codexRenderer struct{}

func (codexRenderer) Target() Target { return TargetCodex }

// codexMCPDocument mirrors claudeCodeMCPDocument. Kept as a separate
// type so a future Codex-specific MCP field (e.g. supports_parallel_tool_calls,
// default_tools_approval_mode — see codex-rs/config/src/mcp_types.rs)
// can be added without polluting the claude_code wire shape.
type codexMCPDocument struct {
	MCPServers map[string]codexMCPServer `json:"mcpServers"`
}

type codexMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (codexRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("codex render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindMCP:
		return renderCodexMCP(spec.MCP)
	case canonical.KindSkill:
		// Codex Agent Skills live under .agents/skills/SKILL.md, parsed
		// by the runtime. Materialising a Claude-Code skill zip into
		// that layout would mis-render — bail.
		return Output{}, ErrUnsupported
	case canonical.KindPlugin:
		// No plugin concept in Codex.
		return Output{}, ErrUnsupported
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	default:
		return Output{}, fmt.Errorf("codex render: unknown kind %q", spec.Kind)
	}
}

func renderCodexMCP(s *canonical.MCPSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("codex render: nil mcp spec")
	}
	doc := codexMCPDocument{MCPServers: make(map[string]codexMCPServer, len(s.Servers))}
	for _, srv := range s.Servers {
		env, err := renderEnvMap(srv.Env)
		if err != nil {
			return Output{}, fmt.Errorf("codex render: server %q: %w", srv.Name, err)
		}
		var args []string
		if len(srv.Args) > 0 {
			args = append(args, srv.Args...)
		}
		doc.MCPServers[srv.Name] = codexMCPServer{
			Command: srv.Command,
			Args:    args,
			Env:     env,
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return Output{}, fmt.Errorf("codex render: marshal: %w", err)
	}
	return Output{Content: body}, nil
}
