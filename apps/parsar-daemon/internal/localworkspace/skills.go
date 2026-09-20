package localworkspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

const CapabilityDirectory = "/environment/initialization/capabilities"
const SkillDirectory = CapabilityDirectory + "/skills"

// VerifySkills consumes the common initialized layout, independently of native loading.
func VerifySkills(skills []agentskill.Metadata) error {
	if len(skills) == 0 {
		return nil
	}
	for _, directory := range []string{CapabilityDirectory, SkillDirectory} {
		actual, err := filepath.EvalSymlinks(directory)
		if err != nil || actual != directory {
			return errors.New("initialized Skill directory unavailable")
		}
	}
	seen := map[string]bool{}
	for _, metadata := range skills {
		if metadata.Type != "inline" || metadata.Name == "" || filepath.Base(metadata.Name) != metadata.Name || metadata.Name == "." || metadata.Name == ".." || seen[metadata.Name] {
			return agentskill.ErrInvalid
		}
		seen[metadata.Name] = true
		root := filepath.Join(SkillDirectory, metadata.Name)
		count, total := 0, int64(0)
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return agentskill.ErrInvalid
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 {
				return agentskill.ErrInvalid
			}
			count++
			total += info.Size()
			if count > agentskill.MaxFiles || total > agentskill.MaxExpandedBytes {
				return agentskill.ErrInvalid
			}
			return nil
		}); err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
		if err != nil || agentskill.ValidateManifest(body, metadata) != nil {
			return agentskill.ErrInvalid
		}
	}
	return nil
}
