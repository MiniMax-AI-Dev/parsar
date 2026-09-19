package mcode

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type prepared struct {
	mu      sync.Mutex
	session *Session
	ready   chan struct{}
	started chan struct{}
	failure error
	closed  bool
	binding *preparedStart
}

type preparedStart struct {
	runID, prompt string
	out           chan<- proto.Envelope
}

func NewPreparationFactory(config WorkspaceConfig) agent.PreparationFactory {
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if req.RunID != "" || req.Prompt != "" || req.ConversationID != "" || req.ObserveSubagentIdentities {
			return nil, fmt.Errorf("mcode: preparation cannot contain input or product context")
		}
		opts, err := prepareWorkspaceOptions(ctx, config, req)
		if err != nil {
			return nil, err
		}
		s, err := launch(ctx, req, opts, config.Binary, nil)
		if err != nil {
			return nil, err
		}
		p := &prepared{session: s, ready: make(chan struct{}), started: make(chan struct{})}
		go s.run(p)
		<-p.ready
		if p.failure != nil {
			<-s.finished
			return nil, p.failure
		}
		if ctx.Err() != nil {
			_ = p.Close()
			return nil, ctx.Err()
		}
		return p, nil
	}
}

func (p *prepared) Start(ctx context.Context, runID, prompt string, out chan<- proto.Envelope) (agent.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.binding != nil {
		return nil, fmt.Errorf("mcode: preparation is no longer available")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(prompt) == "" || out == nil || ctx.Err() != nil {
		return nil, fmt.Errorf("mcode: start requires a live context, identity, prompt and output")
	}
	select {
	case <-p.session.process.Context().Done():
		return nil, fmt.Errorf("mcode: prepared process ended")
	case <-p.session.exited:
		return nil, fmt.Errorf("mcode: prepared process exited")
	default:
	}
	p.binding = &preparedStart{runID: runID, prompt: prompt, out: out}
	close(p.started)
	return p.session, nil
}

func (p *prepared) Close() error {
	p.mu.Lock()
	if p.binding != nil {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	return p.session.Cancel(context.Background())
}

func (p *prepared) Cancel(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return p.session.Cancel(ctx)
}

func (p *prepared) CancellationOutcome() proto.DonePayload { return p.session.CancellationOutcome() }

// The Session goroutine owns preparation, input submission and terminal output.
func (p *prepared) awaitStart(err error) error {
	p.failure = err
	close(p.ready)
	if err != nil {
		return err
	}
	for {
		var ended error
		select {
		case <-p.started:
		case <-p.session.process.Context().Done():
			ended = p.session.process.Context().Err()
		case _, ok := <-p.session.frames:
			if ok {
				continue
			}
			ended = fmt.Errorf("mcode: prepared process exited")
		}
		p.mu.Lock()
		if b := p.binding; b != nil {
			p.session.req.RunID, p.session.req.Prompt, p.session.out = b.runID, b.prompt, b.out
		} else {
			p.closed = true
		}
		p.mu.Unlock()
		return ended
	}
}
