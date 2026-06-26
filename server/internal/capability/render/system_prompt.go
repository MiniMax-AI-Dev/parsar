package render

import (
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// systemPromptDocument is the wire shape emitted by every scaffold's
// renderer for KindSystemPrompt. The daemon never consumes it —
// connector-side resolveSystemPromptCapability folds the prompt into
// agent_options.system_prompt / override_system_prompt directly. The
// renderer call exists only so the renderer factory's default switch
// doesn't reject the kind (mirrors resolveSkillCapability calling
// Render purely for wire-shape consistency).
type systemPromptDocument struct {
	Prompt string `json:"prompt"`
	Mode   string `json:"mode"`
}

func renderSystemPrompt(s *canonical.SystemPromptSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("render: nil system_prompt spec")
	}
	body, err := json.Marshal(systemPromptDocument{
		Prompt: s.Prompt,
		Mode:   string(s.ResolvedMode()),
	})
	if err != nil {
		return Output{}, fmt.Errorf("render: marshal system_prompt: %w", err)
	}
	return Output{Content: body}, nil
}
