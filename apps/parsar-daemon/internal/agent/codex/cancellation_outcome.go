package codex

import (
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type cancellationOutcomeState struct {
	notificationMu sync.Mutex
	mu             sync.Mutex
	terminal       *proto.DonePayload
}

func (s *Session) rememberOutcome(outcome proto.DonePayload) {
	s.usageMu.Lock()
	if outcome.Usage.Provider == "" && s.latestUsage != nil {
		outcome.Usage = proto.Usage{Provider: "openai", InputTokens: int32(s.latestUsage.InputTokens), OutputTokens: int32(s.latestUsage.OutputTokens)}
	}
	s.usageMu.Unlock()
	s.outcome.mu.Lock()
	s.outcome.terminal = &outcome
	s.outcome.mu.Unlock()
}

// CancellationOutcome remains readable after Cancel stops the native process.
func (s *Session) CancellationOutcome() proto.DonePayload {
	s.outcome.notificationMu.Lock()
	defer s.outcome.notificationMu.Unlock()
	s.outcome.mu.Lock()
	terminal := s.outcome.terminal
	s.outcome.mu.Unlock()
	if terminal != nil {
		return *terminal
	}
	outcome := proto.DonePayload{Metadata: map[string]any{}}
	if id := s.currentThreadID(); id != "" {
		outcome.Metadata[proto.DoneMetaAgentSessionID] = id
		outcome.Metadata[proto.DoneMetaAgentSessionType] = "codex_thread"
	}
	s.finalTextMu.Lock()
	outcome.Content = s.finalText
	s.finalTextMu.Unlock()
	s.usageMu.Lock()
	if usage := s.latestUsage; usage != nil {
		outcome.Usage = proto.Usage{Provider: "openai", InputTokens: int32(usage.InputTokens), OutputTokens: int32(usage.OutputTokens)}
	}
	s.usageMu.Unlock()
	return outcome
}
