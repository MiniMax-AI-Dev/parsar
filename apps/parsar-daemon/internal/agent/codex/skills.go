package codex

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func effectiveAgentStateKey(req proto.PromptRequestPayload) string {
	if strings.TrimSpace(req.AgentStateKey) != "" {
		return req.AgentStateKey
	}
	if id := strings.TrimSpace(req.ConversationID); id != "" {
		return "_legacy_conversation/" + id + "/codex"
	}
	if id := strings.TrimSpace(req.RunID); id != "" {
		return "_legacy_run/" + id + "/codex"
	}
	return ""
}

func prepareManagedSkills(ctx context.Context, logger *slog.Logger, req proto.PromptRequestPayload) (string, error) {
	rawSkills := req.AgentOptions["skills"]
	root, err := agent.ManagedSkillsRoot("codex", req.AgentStateKey, req.ConversationID, req.RunID)
	if err != nil {
		return "", fmt.Errorf("codex: resolve managed skills root: %w", err)
	}
	result, err := claudecode.InstallManagedSkills(ctx, logger, root, rawSkills)
	if err != nil {
		return "", fmt.Errorf("codex: install skills: %w", err)
	}
	for _, warning := range result.Warnings {
		logger.Warn("codex: skill install warning", "run_id", req.RunID, "msg", warning)
	}
	if len(result.SkillDirs) == 0 {
		return "", nil
	}
	return root, nil
}

func setSkillExtraRoots(ctx context.Context, rpc *JSONRPCClient, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	_, err := rpc.Request(ctx, "skills/extraRoots/set", SkillsExtraRootsSetParams{ExtraRoots: roots})
	return err
}
