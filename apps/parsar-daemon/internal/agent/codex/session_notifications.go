package codex

import (
	"encoding/json"
	"fmt"
)

func (s *Session) isRootThread(threadID string) bool {
	return threadID != "" && threadID == s.currentThreadID()
}

func (s *Session) isRootTurn(threadID, turnID string) bool {
	if !s.isRootThread(threadID) || turnID == "" || s.terminal.Load() {
		return false
	}
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	return turnID == s.steering.id
}

func (s *Session) beginRootTurn(threadID, turnID string) {
	if !s.startSteering(threadID, turnID) {
		return
	}
	s.beginUsageTurn(turnID)
	s.bufs = NewItemBuffers()
}

func (s *Session) bindTurnResult(raw json.RawMessage) error {
	var res struct {
		Turn Turn `json:"turn"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("codex: decode turn response: %w", err)
	}
	if res.Turn.ID == "" {
		return fmt.Errorf("codex: turn response has missing identity")
	}
	s.steering.mu.Lock()
	turnID := s.steering.id
	s.steering.mu.Unlock()
	if turnID != "" && turnID != res.Turn.ID {
		return fmt.Errorf("codex: turn response does not match the active turn")
	}
	// Native turn/started can precede its RPC reply; duplicate starts retain buffers and usage.
	s.beginRootTurn(s.currentThreadID(), res.Turn.ID)
	return nil
}
