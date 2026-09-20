package v1

import "fmt"

// AgentsCore selects an existing Core harness independently of model identity.
type AgentsCore struct {
	Harness string `json:"harness" enums:"codex,claude_sdk,mcode" binding:"required"`
}

func (x *AgentsCore) Validate() error {
	if x == nil {
		return nil
	}
	switch x.Harness {
	case "codex", "claude_sdk", "mcode":
		return nil
	default:
		return fmt.Errorf("x_agents_core.harness must be codex, claude_sdk or mcode")
	}
}
