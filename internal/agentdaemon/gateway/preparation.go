package gateway

import (
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type preparationSubscription struct {
	sub      *Subscription
	handle   string
	revision uint64
}

// SubscribePreparation correlates private control responses without registering
// a Run. Unsubscribe on abandonment; a terminal resource status closes the stream.
// This subscription belongs to this physical daemon connection only.
func (s *Session) SubscribePreparation(requestID string) (*Subscription, error) {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}
	if requestID == "" || s.preparations[requestID] != nil || len(s.preparations) >= 64 {
		return nil, errors.New("agentdaemon gateway: invalid, duplicate or excess preparation subscription")
	}
	ch := make(chan proto.Envelope, 16)
	sub := &Subscription{Events: ch, ch: ch, durable: true}
	s.preparations[requestID] = &preparationSubscription{sub: sub}
	return sub, nil
}

func (s *Session) UnsubscribePreparation(requestID string) {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	if p := s.preparations[requestID]; p != nil {
		p.sub.mu.Lock()
		p.sub.closeLocked(nil)
		p.sub.mu.Unlock()
		delete(s.preparations, requestID)
	}
}

func (s *Session) dispatchPreparation(env proto.Envelope) {
	var status proto.PreparationStatusPayload
	if env.DecodePayload(&status) != nil {
		return
	}
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	p := s.preparations[env.ID]
	if p == nil {
		return
	}
	if status.State != "rejected" {
		if status.Handle == "" || status.Revision == 0 || (p.handle != "" && p.handle != status.Handle) || status.Revision <= p.revision {
			return
		}
		p.handle, p.revision = status.Handle, status.Revision
	}
	p.sub.mu.Lock()
	defer p.sub.mu.Unlock()
	select {
	case p.sub.ch <- env:
		switch status.State {
		case "started", "released", "expired", "failed":
			p.sub.closeLocked(nil)
		}
	default:
		p.sub.closeLocked(ErrSubscriberOverflow)
	}
	if p.sub.closed {
		delete(s.preparations, env.ID)
	}
}

func (s *Session) closePreparations() {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	for id, p := range s.preparations {
		p.sub.mu.Lock()
		p.sub.closeLocked(ErrSessionClosed)
		p.sub.mu.Unlock()
		delete(s.preparations, id)
	}
}
