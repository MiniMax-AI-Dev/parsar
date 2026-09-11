package gateway

import (
	"errors"
	"fmt"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var ErrSubscriberOverflow = errors.New("execution subscriber buffer overflow")

type Subscription struct {
	Events  <-chan proto.Envelope
	ch      chan proto.Envelope
	mu      sync.Mutex
	err     error
	closed  bool
	durable bool
}

func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Subscription) closeLocked(err error) {
	if !s.closed {
		s.err, s.closed = err, true
		close(s.ch)
	}
}

// Subscribe retains the product's best-effort stream behavior.
func (s *Session) Subscribe(runID string) (<-chan proto.Envelope, error) {
	sub, err := s.subscribe(runID, false)
	if err != nil {
		return nil, err
	}
	return sub.Events, nil
}

// SubscribeDurable fails the subscription on overflow instead of losing events silently.
func (s *Session) SubscribeDurable(runID string) (*Subscription, error) {
	return s.subscribe(runID, true)
}

func (s *Session) subscribe(runID string, durable bool) (*Subscription, error) {
	if runID == "" {
		return nil, fmt.Errorf("agentdaemon gateway: Subscribe requires non-empty runID")
	}
	capacity := 32
	if durable {
		capacity = 256
	}
	ch := make(chan proto.Envelope, capacity)
	sub := &Subscription{Events: ch, ch: ch, durable: durable}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}
	if existing := s.subs[runID]; existing != nil {
		existing.mu.Lock()
		existing.closeLocked(ErrSessionClosed)
		existing.mu.Unlock()
	}
	s.subs[runID] = sub
	s.reg.AttachRun(runID, s)
	return sub, nil
}

func (s *Session) Unsubscribe(runID string) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if sub := s.subs[runID]; sub != nil {
		sub.mu.Lock()
		sub.closeLocked(nil)
		sub.mu.Unlock()
		delete(s.subs, runID)
	}
	s.reg.DetachRun(runID)
}

func (s *Session) closeSubscription(runID string, sub *Subscription, reason string) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	if !sub.durable {
		s.deliverSynthetic(runID, sub.ch, reason)
	}
	sub.closeLocked(ErrSessionClosed)
}

func (s *Session) dispatchToSubscriber(env proto.Envelope) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	sub := s.subs[env.ID]
	if sub == nil {
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	select {
	case sub.ch <- env:
		if env.Type == proto.TypeDone {
			sub.closeLocked(nil)
		}
	default:
		if sub.durable {
			sub.closeLocked(ErrSubscriberOverflow)
		} else {
			s.log("agentdaemon gateway: subscriber buffer full for run %s, dropping %s", env.ID, env.Type)
			if env.Type == proto.TypeDone {
				sub.closeLocked(nil)
			}
		}
	}
	if sub.closed {
		delete(s.subs, env.ID)
		s.reg.DetachRun(env.ID)
	}
}
