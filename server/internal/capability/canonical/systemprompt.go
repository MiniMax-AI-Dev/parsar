package canonical

import (
	"fmt"
	"strings"
)

// SystemPromptMode discriminates how a system_prompt capability is wired
// into the agent's system prompt slot at session-spawn time.
//
//   - Append: prepended to the user-authored agent_options.system_prompt
//     (and to any spec/memory injection), so it acts as a stable
//     workspace-wide pre-instruction.
//   - Override: replaces the system prompt entirely; user prompt and
//     spec/memory are dropped. Standard `--system-prompt` semantics.
type SystemPromptMode string

const (
	SystemPromptModeAppend   SystemPromptMode = "append"
	SystemPromptModeOverride SystemPromptMode = "override"
)

// SystemPromptSpec is the body for Spec{Kind: KindSystemPrompt}.
type SystemPromptSpec struct {
	// Prompt is the raw text that gets injected. No templating.
	Prompt string `json:"prompt"`

	// Mode picks append vs override semantics. Empty defaults to append
	// so older payloads stay valid.
	Mode SystemPromptMode `json:"mode,omitempty"`
}

// Validate enforces a non-empty prompt and a known mode (empty = append).
func (s SystemPromptSpec) Validate() error {
	if strings.TrimSpace(s.Prompt) == "" {
		return fmt.Errorf("%w: prompt is required", ErrInvalidSystemPrompt)
	}
	switch s.Mode {
	case "", SystemPromptModeAppend, SystemPromptModeOverride:
		return nil
	default:
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidSystemPrompt, s.Mode)
	}
}

// ResolvedMode returns the effective mode, treating empty as append.
func (s SystemPromptSpec) ResolvedMode() SystemPromptMode {
	if s.Mode == "" {
		return SystemPromptModeAppend
	}
	return s.Mode
}
