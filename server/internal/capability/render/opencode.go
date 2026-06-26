package render

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// openCodeRenderer emits the JSON shape consumed by OpenCode's runtime.
// The wire shape MUST stay in sync with
// server/internal/connector/opencode/capability_runtime.go
// (capabilityMCPContent / capabilityMCPServer) or the connector will fail
// to decode rendered output.
type openCodeRenderer struct{}

func (openCodeRenderer) Target() Target { return TargetOpenCode }

type openCodeMCPDocument struct {
	MCPServers map[string]openCodeMCPServer `json:"mcpServers"`
}

// openCodeMCPServer mirrors capability_runtime.go's capabilityMCPServer.
// Enabled is always true — per-server enable/disable is not modeled in
// canonical.Spec; every server in a Spec is wanted.
type openCodeMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

func (openCodeRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("opencode render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindMCP:
		return renderOpenCodeMCP(spec.MCP)
	case canonical.KindSkill:
		// OpenCode materializes skills via git clone at session-spawn time
		// (see capability_runtime.go buildSkillCloneCommand).
		return Output{}, ErrUnsupported
	case canonical.KindPlugin:
		// Plugins are Claude Code only; OpenCode has no plugin concept.
		return Output{}, ErrUnsupported
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	default:
		return Output{}, fmt.Errorf("opencode render: unknown kind %q", spec.Kind)
	}
}

func renderOpenCodeMCP(s *canonical.MCPSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("opencode render: nil mcp spec")
	}
	doc := openCodeMCPDocument{MCPServers: make(map[string]openCodeMCPServer, len(s.Servers))}
	for _, srv := range s.Servers {
		env, err := renderEnvMap(srv.Env)
		if err != nil {
			return Output{}, fmt.Errorf("opencode render: server %q: %w", srv.Name, err)
		}
		// Defensive copy: Args order is meaningful (CLI positionals); avoid aliasing.
		var args []string
		if len(srv.Args) > 0 {
			args = append(args, srv.Args...)
		}
		doc.MCPServers[srv.Name] = openCodeMCPServer{
			Command: srv.Command,
			Args:    args,
			Env:     env,
			Enabled: true,
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return Output{}, fmt.Errorf("opencode render: marshal: %w", err)
	}
	return Output{Content: body}, nil
}
