package codex

import (
	"fmt"
	"io/fs"
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
		if err := verifyHostedSkillLayout(filepath.Join(localworkspace.SkillDirectory, skill.Name)); err != nil {
			return err
		}
	}
	return nil
}

func verifyHostedSkillLayout(root string) error {
	// Native dependency declarations may start MCP installation outside the
	// workspace tool sandbox. They are not qualified by an inert Skill upload.
	if _, err := os.Lstat(filepath.Join(root, "agents", "openai.yaml")); err == nil {
		return fmt.Errorf("codex: native Skill configuration is unsupported")
	} else if !os.IsNotExist(err) {
		return err
	}
	// Extra roots are scanned recursively. One public Skill must not silently
	// introduce additional native Skills with their own activation metadata.
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "SKILL.md" && path != filepath.Join(root, "SKILL.md") {
			return fmt.Errorf("codex: nested native Skills are unsupported")
		}
		return nil
	})
}
