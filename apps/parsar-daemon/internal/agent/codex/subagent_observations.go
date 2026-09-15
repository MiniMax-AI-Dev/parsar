package codex

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const subagentLookupTimeout = 3 * time.Second

type subagentCandidate struct{ child, parent, turn, item string }

// One worker owns bounded metadata reads; the native reader only admits facts.
type subagentObservations struct {
	mu        sync.Mutex
	seen      map[string]bool
	sealed    bool
	queue     chan subagentCandidate
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	published chan struct{}
}

func (s *Session) startSubagentObservations() {
	ctx, cancel := context.WithCancel(s.cancelCtx)
	s.subagents = &subagentObservations{seen: make(map[string]bool), queue: make(chan subagentCandidate, 64),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), published: make(chan struct{})}
	go s.collectSubagentIdentities()
}

func (s *Session) observeSubagentIdentity(raw json.RawMessage) {
	o := s.subagents
	if o == nil {
		return
	}
	var event struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			ID                string   `json:"id"`
			Type              string   `json:"type"`
			Tool              string   `json:"tool"`
			Status            string   `json:"status"`
			SenderThreadID    string   `json:"senderThreadId"`
			ReceiverThreadIDs []string `json:"receiverThreadIds"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Item.Type != "collabAgentToolCall" ||
		(event.Item.Tool != "spawnAgent" && event.Item.Tool != "resumeAgent") {
		return
	}
	if !s.isRootTurn(event.ThreadID, event.TurnID) || event.Item.SenderThreadID != event.ThreadID || event.Item.ID == "" || event.Item.Status != "completed" {
		s.subagentGap("discovery_unverified_or_late")
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, child := range event.Item.ReceiverThreadIDs {
		if o.sealed || s.terminal.Load() || child == "" || child == event.ThreadID {
			s.subagentGap("discovery_unverified_or_late")
			continue
		}
		if o.seen[child] {
			continue
		}
		if len(o.seen) == 64 {
			s.subagentGap("discovery_capacity")
			continue
		}
		o.seen[child] = true
		o.queue <- subagentCandidate{child, event.ThreadID, event.TurnID, event.Item.ID}
	}
}

func (s *Session) collectSubagentIdentities() {
	o := s.subagents
	defer close(o.done)
	budget := 64
	for {
		select {
		case <-o.ctx.Done():
			s.subagentGap("discovery_owner_ended")
			return
		case candidate, open := <-o.queue:
			if !open {
				return
			}
			s.resolveSubagentIdentity(candidate, &budget)
		}
	}
}

func (s *Session) resolveSubagentIdentity(candidate subagentCandidate, budget *int) {
	ctx, cancel := context.WithTimeout(s.subagents.ctx, subagentLookupTimeout)
	defer cancel()
	for {
		metadata, found, err := s.persistedSubagent(ctx, candidate.child, candidate.parent, budget)
		if err != nil {
			s.subagentGap(err.Error())
			return
		}
		if found {
			env, err := proto.NewEnvelope(proto.TypeSubagentIdentity, s.runID, proto.SubagentIdentityPayload{
				NativeID: metadata.ID, ParentNativeID: metadata.ParentThreadID, NativeCreatedAt: metadata.CreatedAt,
				ParentTurnID: candidate.turn, SourceItemID: candidate.item,
			})
			if err != nil || !s.sendWithin(ctx, env) {
				s.subagentGap("identity_delivery_unavailable")
			}
			return
		}
		select {
		case <-ctx.Done():
			s.subagentGap("metadata_not_persisted")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Session) subagentGap(reason string) {
	s.cfg.logger.Warn("codex: subagent identity observation incomplete", "run_id", s.runID, "reason", reason)
}

// The reader has already frozen terminal content/usage and sealed root mutation.
// It must remain free to read pending RPC replies while this worker settles.
func (s *Session) sendTerminal(events ...proto.Envelope) {
	emit := func() {
		for _, event := range events {
			s.trySend(event)
		}
	}
	o := s.subagents
	if o == nil {
		emit()
		return
	}
	o.mu.Lock()
	o.sealed = true
	close(o.queue)
	empty := len(o.seen) == 0
	o.mu.Unlock()
	finish := func() {
		timer := time.AfterFunc(subagentLookupTimeout, o.cancel)
		<-o.done
		timer.Stop()
		o.cancel()
		emit()
		s.closeOut()
		close(o.published)
	}
	if empty {
		// No metadata read can depend on this reader. Startup failures still emit
		// synchronously before run's deferred output cleanup.
		finish()
	} else {
		go finish()
	}
}

func (s *Session) closeRunOutput() {
	if o := s.subagents; o != nil {
		o.mu.Lock()
		sealed := o.sealed
		o.mu.Unlock()
		if sealed {
			<-o.published
		} else {
			o.cancel()
			<-o.done
		}
	}
	s.closeOut()
}
