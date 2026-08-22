package deepseekharness

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type SessionConfigForTest struct {
	Binary      string
	ExtraArgs   []string
	KillTimeout time.Duration
}

func NewSessionForTest(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg SessionConfigForTest) (*Session, error) {
	return newSession(ctx, req, out, sessionConfig{
		binary:      cfg.Binary,
		extraArgs:   cfg.ExtraArgs,
		killTimeout: cfg.KillTimeout,
	})
}

type Translator translator

func NewTranslatorForTest(runID string) *Translator { return (*Translator)(newTranslator(runID)) }

func (t *Translator) AppendLine(line string) { (*translator)(t).appendLine(line) }

func (t *Translator) TerminalEnvelopes(waitErr error, stderr string, cancelled bool) []proto.Envelope {
	return (*translator)(t).terminalEnvelopes(waitErr, stderr, cancelled)
}

func RenderPatchForTest(raw any, model, provider string) ([]byte, error) {
	cfg, hasProvider, err := normaliseProvider(raw)
	if err != nil {
		return nil, err
	}
	return renderPatch(cfg, hasProvider, model, provider)
}

func ResolveHomeForTest(agentStateKey, conversationID, runID string) (string, error) {
	return resolveHome(agentStateKey, conversationID, runID)
}

const (
	HomeEnvVarForTest   = dshHomeEnvVar
	ManagedRouteForTest = managedRoute
)
