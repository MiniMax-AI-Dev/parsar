package codex

import "github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"

// CancellationOutcome remains readable after Cancel stops the native process.
func (s *Session) CancellationOutcome() proto.DonePayload {
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
