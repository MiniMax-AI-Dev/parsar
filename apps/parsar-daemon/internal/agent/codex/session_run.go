package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) run(plan SessionPlan, req proto.PromptRequestPayload) {
	defer close(s.waitDone)
	defer s.stopCodexInteractionTimers()
	defer s.stopFunctionCalls()
	defer s.cleanup()
	defer s.closeOut()

	if err := s.resolveThread(req, plan); err != nil {
		s.emitTerminal(err.Error(), true)
		return
	}

	input := FirstUserInput(req.Prompt)
	if len(input) == 0 {
		s.emitTerminal("codex: empty prompt", true)
		return
	}
	turnParams := TurnStartParams{
		ThreadID:     s.currentThreadID(),
		Input:        input,
		Environments: plan.Environments,
	}
	if plan.CollaborationMode != "" {
		model := strings.TrimSpace(s.resolvedModel)
		if model == "" {
			s.emitTerminal("codex: collaboration mode requires a resolved model", true)
			return
		}
		var developerInstructions *string
		if plan.SystemPrompt != "" {
			developerInstructions = &plan.SystemPrompt
		}
		turnParams.CollaborationMode = &CollaborationMode{
			Mode: plan.CollaborationMode,
			Settings: CollaborationModeSettings{
				Model:                 model,
				DeveloperInstructions: developerInstructions,
			},
		}
	}
	turnCtx, turnCancel := context.WithTimeout(s.cancelCtx, 10*time.Second)
	_, ackErr := s.rpc.requestWithResult(turnCtx, "turn/start", turnParams, s.bindTurnResult)
	turnCancel()
	if ackErr != nil {
		s.cfg.logger.Warn("codex: turn/start ack failed", "run_id", s.runID, "err", ackErr)
		s.emitTerminal(fmt.Sprintf("codex: turn/start: %v", ackErr), true)
		return
	}

	// Block until terminal handlers close the RPC child or cancellation arrives.
	select {
	case <-s.rpc.Done():
		if !s.cancelled.Load() && s.cancelCtx.Err() == nil {
			s.emitTerminal("codex: connection closed before the run completed", true)
		}
	case <-s.cancelCtx.Done():
		_ = s.rpc.Close()
	}
}
