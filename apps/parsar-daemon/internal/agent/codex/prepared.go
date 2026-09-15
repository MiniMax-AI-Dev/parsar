package codex

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Prepared owns a connected native resource until Start transfers it to a Session.
// It observes owner cancellation and RPC exit, not continuous executor readiness.
// Remote status is rechecked at Start without reconnecting the prepared resource.
type Prepared struct {
	mu           sync.Mutex
	session      *Session
	plan         SessionPlan
	remote       bool
	resumeID     string
	strictResume bool
	claimed      bool
	closed       bool
	started      bool
	transferred  chan struct{}
}

// Start consumes the preparation once. ctx bounds only this start operation;
// cancellation after return does not cancel the transferred Session. The original
// owner context remains its lifetime context. On success the Session owns out.
func (p *Prepared) Start(ctx context.Context, runID, prompt string, out chan<- proto.Envelope) (agent.Session, error) {
	session, err := p.start(ctx, runID, prompt, out)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (p *Prepared) start(ctx context.Context, runID, prompt string, out chan<- proto.Envelope) (*Session, error) {
	if out == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(prompt) == "" {
		return nil, errors.New("codex: start requires a run identity, prompt and output channel")
	}
	p.mu.Lock()
	if p.claimed || p.closed {
		p.mu.Unlock()
		return nil, errors.New("codex: preparation is no longer available")
	}
	p.claimed = true
	p.mu.Unlock()

	transferred := false
	defer func() {
		if !transferred {
			_ = p.Close()
		}
	}()
	if p.remote {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		status, err := nativeEnvironmentStatus(check, p.session.rpc, "remote")
		cancel()
		if err != nil || status != "ready" {
			return nil, errors.New("codex: prepared remote environment is no longer ready")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || ctx.Err() != nil || p.session.cancelCtx.Err() != nil || !p.session.rpc.Alive() {
		return nil, errors.New("codex: prepared harness is no longer available")
	}
	s := p.session
	s.runID, s.out = runID, out
	if s.observeSubagentIdentities {
		s.startSubagentObservations()
	}
	s.registerHandlers()
	p.started = true
	close(p.transferred)
	transferred = true
	req := proto.PromptRequestPayload{RunID: runID, Prompt: prompt, AgentSessionID: p.resumeID, StrictResume: p.strictResume}
	go s.run(p.plan, req)
	return s, nil
}

// Close waits for unused teardown and plan cleanup, including another caller's
// ongoing Close. After successful Start it is inert; use
// the returned Session's cancellation path to release the transferred resource.
func (p *Prepared) Close() error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	p.session.cancelFn()
	err := p.session.rpc.Close()
	p.plan.Cleanup()
	return err
}

func (p *Prepared) watchOwner() {
	select {
	case <-p.session.cancelCtx.Done():
	case <-p.session.rpc.Done():
	case <-p.transferred:
		return
	}
	_ = p.Close()
}
