package claudecode

// Parallel to export_test.go — exposes the subprocess session
// constructor + config knobs for session_test.go.

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type SessionConfigForTest struct {
	ClaudeBinary string
	ExtraArgs    []string
	KillTimeout  time.Duration
	AskTimeout   time.Duration
}

func NewSessionForTest(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg SessionConfigForTest) (*Session, error) {
	return newSession(ctx, req, out, sessionConfig{
		claudeBinary: cfg.ClaudeBinary,
		extraArgs:    cfg.ExtraArgs,
		killTimeout:  cfg.KillTimeout,
		askTimeout:   cfg.AskTimeout,
	})
}

// SubmitPromptForUserChoiceForTest exposes the ask-decision writer so
// session_test can drive the answer-resume path without a separate
// dispatch hop.
func (s *Session) SubmitPromptForUserChoiceForTest(askID string, decision proto.PromptForUserChoiceDecisionPayload) error {
	return s.SubmitPromptForUserChoice(context.Background(), askID, decision)
}
