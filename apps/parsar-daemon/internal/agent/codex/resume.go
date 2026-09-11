package codex

import (
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) resolveThread(req proto.PromptRequestPayload, plan SessionPlan) error {
	if strings.TrimSpace(req.AgentSessionID) != "" {
		if err := s.resumeThread(req.AgentSessionID, plan); err == nil {
			return nil
		} else if req.StrictResume {
			return fmt.Errorf("codex: thread/resume: %w", err)
		} else {
			s.cfg.logger.Warn("codex: thread/resume failed; starting fresh", "run_id", s.runID, "thread_id", req.AgentSessionID, "err", err)
		}
	}
	if err := s.startThread(plan); err != nil {
		return fmt.Errorf("codex: thread/start: %w", err)
	}
	return nil
}
