package render

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// piRenderer serializes capability specs for the pi agent runtime
// (`pi --mode json`). pi consumes Agent Skills via the same SKILL.md
// standard as Claude Code, so KindSkill reuses renderClaudeCodeSkill to
// keep the descriptor byte-identical — the daemon decodes pi and
// claudecode skills with one claudeCodeSkillDocument unmarshal. Managed
// MCP and plugin delivery are out of scope for pi, so both return
// ErrUnsupported, which the agentdaemon connector treats as a soft
// degrade (skip + disabled-capability notice). This makes pi the mirror
// of codex: codex renders MCP and rejects Skill; pi renders Skill and
// rejects MCP.
type piRenderer struct{}

func (piRenderer) Target() Target { return TargetPi }

func (piRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("pi render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindSkill:
		return renderClaudeCodeSkill(spec.Skill)
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	case canonical.KindMCP:
		return Output{}, ErrUnsupported
	case canonical.KindPlugin:
		return Output{}, ErrUnsupported
	default:
		return Output{}, fmt.Errorf("pi render: unknown kind %q", spec.Kind)
	}
}
