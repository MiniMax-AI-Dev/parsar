// Package render translates a normalized capability spec (see
// capability/canonical) into a scaffold-specific rendered blob that
// downstream agent runtimes consume.
//
// Each renderer is pure: no DB, no secrets, no per-user credentials.
// The JSON it produces still carries opaque placeholders for any value
// that has to be resolved at session-spawn time:
//
//	${PARSAR_SECRET:<secret_id>}                  — org-scoped secret
//	${PARSAR_CREDENTIAL:<kind_code>}              — per-user credential by kind (caller's user_id)
//
// The runtime layer (server/internal/connector/...) substitutes those
// placeholders with decrypted plaintext just before launching the agent.
package render

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// Target enumerates the agent scaffolds the renderer can serialize for.
type Target string

const (
	TargetOpenCode   Target = "opencode"
	TargetClaudeCode Target = "claudecode"
	TargetCodex      Target = "codex"
)

// Output is what a Renderer returns. Content is the scaffold-specific JSON
// document. Warnings collects non-fatal issues — their presence does NOT
// make Render() return an error.
type Output struct {
	Content  []byte
	Warnings []string
}

// ErrUnsupported indicates the renderer cannot serialize this Spec for its
// Target (e.g. OpenCode/Codex can't render Skill or Plugin). Callers should
// treat it as "skip this capability for this target".
var ErrUnsupported = errors.New("render: spec kind unsupported for target")

// Renderer implementations must be pure and side-effect free.
type Renderer interface {
	Target() Target
	Render(ctx context.Context, spec canonical.Spec) (Output, error)
}

func For(target Target) (Renderer, error) {
	switch target {
	case TargetOpenCode:
		return openCodeRenderer{}, nil
	case TargetClaudeCode:
		return claudeCodeRenderer{}, nil
	case TargetCodex:
		return codexRenderer{}, nil
	default:
		return nil, fmt.Errorf("render: unknown target %q", target)
	}
}
