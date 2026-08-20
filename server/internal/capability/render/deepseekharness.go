package render

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// deepseekHarnessRenderer serializes capability specs for the DeepSeek
// Harness runtime. Sandbox daemons use the resident profile, which installs
// managed Skill archives and translates MCP entries into dsh-mcp-client rows.
// Local-device headless daemons still reject those options at the adapter
// boundary because exposing a resident unauthenticated server there is unsafe.
type deepseekHarnessRenderer struct{}

func (deepseekHarnessRenderer) Target() Target { return TargetDeepseekHarness }

func (deepseekHarnessRenderer) Supports(kind canonical.Kind) bool {
	return kind == canonical.KindMCP || kind == canonical.KindSkill || kind == canonical.KindSystemPrompt
}

func (deepseekHarnessRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("deepseek_harness render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindMCP:
		return renderClaudeCodeMCP(spec.MCP)
	case canonical.KindSkill:
		return renderClaudeCodeSkill(spec.Skill)
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	case canonical.KindPlugin:
		return Output{}, ErrUnsupported
	default:
		return Output{}, fmt.Errorf("deepseek_harness render: unknown kind %q", spec.Kind)
	}
}
