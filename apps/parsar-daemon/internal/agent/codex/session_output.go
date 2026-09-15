package codex

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) trySend(env proto.Envelope) {
	ctx, cancel := context.WithTimeout(s.cancelCtx, terminalSendTimeout)
	defer cancel()
	if !s.sendWithin(ctx, env) {
		s.cfg.logger.Warn("codex: out send unavailable", "type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) sendWithin(ctx context.Context, env proto.Envelope) bool {
	s.outMu.RLock()
	defer s.outMu.RUnlock()
	if s.outClosed {
		return false
	}
	select {
	case s.out <- env:
		return true
	case <-s.cancelCtx.Done():
	case <-ctx.Done():
	}
	return false
}
