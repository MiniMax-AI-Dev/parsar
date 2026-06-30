package pi

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type SessionConfigForTest struct {
	PiBinary    string
	ExtraArgs   []string
	KillTimeout time.Duration
}

func NewSessionForTest(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg SessionConfigForTest) (*Session, error) {
	return newSession(ctx, req, out, sessionConfig{
		piBinary:    cfg.PiBinary,
		extraArgs:   cfg.ExtraArgs,
		killTimeout: cfg.KillTimeout,
	})
}

type Translator translator

type Translation = translation

func NewTranslatorForTest(runID string) *Translator { return (*Translator)(newTranslator(runID)) }

func (t *Translator) Translate(line []byte) (Translation, error) {
	return (*translator)(t).Translate(line)
}

func (t *Translator) TerminalEnvelopes(waitErr error, stderr string, cancelled bool) []proto.Envelope {
	return (*translator)(t).terminalEnvelopes(waitErr, stderr, cancelled)
}
