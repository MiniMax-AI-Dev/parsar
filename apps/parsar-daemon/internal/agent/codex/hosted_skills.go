package codex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

func verifyHostedSkills(skills []agentskill.Metadata) error {
	if err := localworkspace.VerifySkills(skills); err != nil {
		return err
	}
	for _, skill := range skills {
		// Native dependency declarations may start MCP installation outside the
		// workspace tool sandbox. They are not qualified by an inert Skill upload.
		_, err := os.Lstat(filepath.Join(localworkspace.SkillDirectory, skill.Name, "agents", "openai.yaml"))
		if err == nil {
			return fmt.Errorf("codex: native Skill configuration is unsupported")
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
