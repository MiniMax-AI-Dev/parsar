package claudecode

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/skillinstall"
)

type skillDescriptor = skillinstall.Descriptor

type SkillInstallResult struct {
	Warnings []string
}

func installSkills(
	ctx context.Context,
	logger *slog.Logger,
	workDir string,
	skills []skillDescriptor,
) (SkillInstallResult, error) {
	if strings.TrimSpace(workDir) == "" {
		return SkillInstallResult{}, fmt.Errorf("claudecode skills: workDir is required")
	}
	result, err := skillinstall.Install(ctx, logger, filepath.Join(workDir, ".claude", "skills"), skills)
	if err != nil {
		return SkillInstallResult{}, err
	}
	return SkillInstallResult{Warnings: result.Warnings}, nil
}

func decodeSkillDescriptors(raw any) ([]skillDescriptor, []string) {
	return skillinstall.Decode(raw)
}
