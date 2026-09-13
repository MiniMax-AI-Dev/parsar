package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type steeringTurn struct {
	mu      sync.Mutex
	id      string
	stopped bool
	cancel  context.CancelFunc
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
	return s.steer(ctx, input, nil)
}

// SteerWithReceipt waits under the Run context after reporting the complete write.
func (s *Session) SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	return s.steer(ctx, input, written)
}

func (s *Session) steer(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.steering.mu.Lock()
	turnID, stopped := s.steering.id, s.steering.stopped
	if !stopped {
		s.steering.cancel = cancel
	}
	s.steering.mu.Unlock()
	defer func() {
		s.steering.mu.Lock()
		s.steering.cancel = nil
		s.steering.mu.Unlock()
	}()
	if stopped || s.cancelCtx.Err() != nil {
		return agent.ErrSteeringInactive
	}
	threadID := s.currentThreadID()
	if turnID == "" || threadID == "" {
		return agent.ErrSteeringNotReady
	}
	params := TurnSteerParams{ThreadID: threadID, ExpectedTurnID: turnID, Input: FirstUserInput(input.Text)}
	timeout := s.rpc.cfg.RequestTimeout
	if written != nil {
		timeout = 0
	}
	raw, err := s.rpc.requestWithTimeout(ctx, "turn/steer", params, func(frame any) error {
		if err := s.rpc.writeFrameContext(ctx, frame); err != nil {
			return err
		}
		if written != nil {
			written()
		}
		return nil
	}, timeout)
	if err != nil {
		var rejected *JsonRpcError
		if errors.As(err, &rejected) {
			return fmt.Errorf("%w: %v", agent.ErrSteeringRejected, err)
		}
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
	if s.steering.cancel != nil {
		s.steering.cancel()
	}
}
