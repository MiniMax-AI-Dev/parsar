package render

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// deepseekHarnessRenderer serializes capability specs for the DeepSeek
// Harness runtime (`dsh --profile headless`). That surface takes a task
// string and a config overlay only: the daemon adapter folds a rendered
// system prompt into the task text, while skills, managed MCP servers and
// plugins have no seam there and return ErrUnsupported, which the
// agentdaemon connector treats as a soft degrade (skip + disabled-capability
// notice).
type deepseekHarnessRenderer struct{}

func (deepseekHarnessRenderer) Target() Target { return TargetDeepseekHarness }

func (deepseekHarnessRenderer) Supports(kind canonical.Kind) bool {
	return kind == canonical.KindSystemPrompt
}

func (deepseekHarnessRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("deepseek_harness render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	case canonical.KindSkill, canonical.KindMCP, canonical.KindPlugin:
		return Output{}, ErrUnsupported
	default:
		return Output{}, fmt.Errorf("deepseek_harness render: unknown kind %q", spec.Kind)
	}
}
