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

func (s *Session) rememberOutcome(outcome proto.DonePayload) proto.DonePayload {
	s.usageMu.Lock()
	if outcome.Usage.Provider == "" && s.latestUsage != nil {
		outcome.Usage = s.usagePayload(*s.latestUsage)
	}
	s.usageMu.Unlock()
	s.outcome.mu.Lock()
	s.outcome.terminal = &outcome
	s.outcome.mu.Unlock()
	return outcome
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
		outcome.Usage = s.usagePayload(*usage)
	}
	s.usageMu.Unlock()
	return outcome
}
