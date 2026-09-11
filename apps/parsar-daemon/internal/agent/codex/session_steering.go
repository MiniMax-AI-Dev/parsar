package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type steeringTurn struct {
	mu      sync.Mutex
	id      string
	stopped bool
}

// TurnSteerParams mirrors the native Codex app-server turn/steer request.
type TurnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []UserInput `json:"input"`
}

var _ agent.Steerer = (*Session)(nil)

// Steer returns success only after Codex accepts input for this native turn.
func (s *Session) Steer(ctx context.Context, input proto.PromptSteerPayload) error {
	s.steering.mu.Lock()
	turnID, stopped := s.steering.id, s.steering.stopped
	s.steering.mu.Unlock()
	if stopped || s.cancelCtx.Err() != nil {
		return agent.ErrSteeringInactive
	}
	threadID := s.currentThreadID()
	if turnID == "" || threadID == "" {
		return agent.ErrSteeringNotReady
	}
	params := TurnSteerParams{ThreadID: threadID, ExpectedTurnID: turnID, Input: FirstUserInput(input.Text)}
	raw, err := s.rpc.Request(ctx, "turn/steer", params)
	if err != nil {
		return err
	}
	var result struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("codex: decode steering receipt: %w", err)
	}
	if result.TurnID != turnID {
		return fmt.Errorf("codex: steering receipt does not match the active turn")
	}
	return nil
}

func (s *Session) startSteering(raw json.RawMessage) {
	var notification TurnStartedNotification
	if json.Unmarshal(raw, &notification) != nil || notification.ThreadID != s.currentThreadID() {
		return
	}
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	if !s.steering.stopped {
		s.steering.id = notification.Turn.ID
	}
}

func (s *Session) stopSteering() {
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	s.steering.stopped = true
}
