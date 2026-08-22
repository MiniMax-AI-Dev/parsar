package pi

import (
	"context"
	"log/slog"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/skillinstall"
)

type skillDescriptor = skillinstall.Descriptor
type SkillInstallResult = skillinstall.Result

func installSkills(ctx context.Context, logger *slog.Logger, root string, skills []skillDescriptor) (SkillInstallResult, error) {
	return skillinstall.Install(ctx, logger, root, skills)
}

func decodeSkillDescriptors(raw any) ([]skillDescriptor, []string) {
	return skillinstall.Decode(raw)
}

func resolveSkillsRoot(conversationID, runID string) (string, error) {
	return skillinstall.ResolveRoot("pi", conversationID, runID)
}

func mergeSkillDirs(existing any, resolved []string) []string {
	return skillinstall.MergeDirs(existing, resolved)
}

func cloneAgentOptions(opts map[string]any) map[string]any {
	return skillinstall.CloneOptions(opts)
}
