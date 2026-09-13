package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type pendingInput struct {
	id      string
	receipt chan error
}

type steeringState struct {
	mu        sync.Mutex
	sessionID string
	closed    bool
	pending   *pendingInput
	seen      map[string]bool
}

var _ agent.Steerer = (*session)(nil)

// Steer waits for native consumption, which may occur in a later native turn
// within this one SDK query. A completed stdin write is not a receipt.
func (s *session) Steer(ctx context.Context, input proto.PromptSteerPayload) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(input.InputID) == "" || len(input.InputID) > 256 || strings.TrimSpace(input.Text) == "" {
		return fmt.Errorf("%w: input identity and text are required", agent.ErrSteeringRejected)
	}
	data, err := json.Marshal(struct {
		Type    string `json:"type"`
		InputID string `json:"input_id"`
		Text    string `json:"text"`
	}{Type: "steer", InputID: input.InputID, Text: input.Text})
	if err != nil || len(data) > 1024*1024 {
		return fmt.Errorf("%w: input exceeds bridge limit", agent.ErrSteeringRejected)
	}
	s.steering.mu.Lock()
	if s.steering.closed {
		s.steering.mu.Unlock()
		return agent.ErrSteeringInactive
	}
	if s.steering.sessionID == "" {
		s.steering.mu.Unlock()
		return agent.ErrSteeringNotReady
	}
	if s.steering.pending != nil || s.steering.seen[input.InputID] || len(s.steering.seen) >= 63 {
		s.steering.mu.Unlock()
		return fmt.Errorf("%w: input is pending, repeated or over capacity", agent.ErrSteeringRejected)
	}
	if err := ctx.Err(); err != nil {
		s.steering.mu.Unlock()
		return err
	}
	if s.steering.seen == nil {
		s.steering.seen = map[string]bool{}
	}
	pending := &pendingInput{id: input.InputID, receipt: make(chan error, 1)}
	s.steering.seen[input.InputID] = true
	s.steering.pending = pending
	s.steering.mu.Unlock()
	// Cancellation must release a blocked write, but a lost receipt after a full
	// write preserves the process and unknown outcome without automatic redelivery.
	stop := context.AfterFunc(ctx, s.process.Cancel)
	s.writeMu.Lock()
	if err = ctx.Err(); err == nil {
		_, err = s.process.Stdin.Write(append(data, '\n'))
	}
	s.writeMu.Unlock()
	stop()
	if err != nil {
		s.process.Cancel()
		return fmt.Errorf("claudesdk: input transport failed")
	}
	select {
	case err := <-pending.receipt:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) receiveInput(event bridgeEvent, start startRequest) error {
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	switch event.Type {
	case "input_ready":
		if s.steering.closed || s.steering.sessionID != "" || event.SessionID == "" || start.Resume != "" && event.SessionID != start.Resume {
			return fmt.Errorf("claudesdk: invalid input session identity")
		}
		s.steering.sessionID = event.SessionID
	case "input_closed":
		if s.steering.closed || s.steering.sessionID == "" || event.SessionID != s.steering.sessionID {
			return fmt.Errorf("claudesdk: invalid input closure")
		}
		s.steering.closed = true
	case "input_applied", "input_rejected":
		pending := s.steering.pending
		if pending == nil || pending.id != event.InputID {
			return fmt.Errorf("claudesdk: invalid input receipt")
		}
		var err error
		if event.Type == "input_rejected" {
			err = agent.ErrSteeringRejected
		}
		pending.receipt <- err
		s.steering.pending = nil
	}
	return nil
}

func (s *session) steeringComplete() bool {
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	return s.steering.pending == nil
}

func (s *session) stopSteering() {
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	s.steering.closed = true
	if s.steering.pending != nil {
		s.steering.pending.receipt <- fmt.Errorf("claudesdk: execution ended with unknown input outcome")
		s.steering.pending = nil
	}
}

func (s *session) matchesInputSession(id string) bool {
	s.steering.mu.Lock()
	defer s.steering.mu.Unlock()
	return s.steering.sessionID == "" || s.steering.sessionID == id
}
