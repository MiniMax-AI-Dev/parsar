package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type prepared struct {
	mu      sync.Mutex
	session *session
	ready   chan struct{}
	started chan struct{}
	binding *preparedStart
	closed  bool
	failure error
}

type preparedStart struct {
	runID  string
	prompt string
	out    chan<- proto.Envelope
}

var _ agent.PreparedCancellation = (*prepared)(nil)

// NewPreparationFactory retains a trusted workspace process without submitting model input.
func NewPreparationFactory(config Config) agent.PreparationFactory {
	return func(owner context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
		if owner == nil {
			owner = context.Background()
		}
		if config.Workspace == nil || req.RunID != "" || req.Prompt != "" || req.ConversationID != "" || req.ObserveSubagentIdentities {
			return nil, fmt.Errorf("claudesdk: preparation requires workspace configuration without input, conversation or subagents")
		}
		snapshot := config
		snapshot.Env = slices.Clone(config.Env)
		workspace := *config.Workspace
		workspace.ProtectedDirs = slices.Clone(workspace.ProtectedDirs)
		snapshot.Workspace = &workspace
		start, env, err := prepareConfiguration(snapshot, req)
		if err != nil {
			return nil, err
		}
		start.Type = "prepare"
		info, err := CheckRuntime(owner, snapshot)
		if err != nil || !info.supportsWorkspacePreparation() {
			return nil, fmt.Errorf("claudesdk: packaged runtime does not support workspace preparation")
		}
		if start.observeFunctions && !info.supportsWorkspaceCommands() {
			return nil, fmt.Errorf("claudesdk: packaged runtime does not support workspace command observations")
		}
		s, err := launch(owner, snapshot, start, env)
		if err != nil {
			return nil, err
		}
		s.reads.supported = slices.Contains(info.Features, "workspace_read")
		p := &prepared{session: s, ready: make(chan struct{}), started: make(chan struct{})}
		go s.run(owner, "", start, nil, p)
		select {
		case <-p.ready:
			if owner.Err() == nil {
				return p, nil
			}
			_ = p.Close()
			return nil, owner.Err()
		case <-s.settled:
			return nil, p.failure
		}
	}
}

// Start transfers ownership once; ctx bounds this operation, not the Session lifetime.
func (p *prepared) Start(ctx context.Context, runID, prompt string, out chan<- proto.Envelope) (agent.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.closed || p.binding != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("claudesdk: preparation is no longer available")
	}
	var err error
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(prompt) == "" || out == nil {
		err = fmt.Errorf("claudesdk: start requires a run identity, prompt and output channel")
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if p.session.process.Context().Err() != nil {
		err = p.session.process.Context().Err()
	} else {
		select {
		case <-p.session.process.Done():
			err = fmt.Errorf("claudesdk: prepared process has exited")
		case <-p.session.settled:
			err = fmt.Errorf("claudesdk: preparation has ended")
		default:
		}
	}
	if err != nil {
		p.closed = true
		p.mu.Unlock()
		_ = p.session.Cancel(context.Background())
		return nil, err
	}
	p.binding = &preparedStart{runID: runID, prompt: prompt, out: out}
	close(p.started)
	p.mu.Unlock()
	return p.session, nil
}

// Close settles unused ownership and is inert after transfer.
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

// Cancel follows the resource across transfer and waits for the existing output settlement.
func (p *prepared) Cancel(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return p.session.Cancel(ctx)
}

func (p *prepared) CancellationOutcome() proto.DonePayload {
	return p.session.CancellationOutcome()
}

func (p *prepared) awaitStart(scanner *bridgeOutput, failure error) (*preparedStart, error) {
	if failure == nil {
		if scanner.Scan() {
			var event bridgeEvent
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				failure = fmt.Errorf("claudesdk: invalid SDK bridge output")
			} else if event.Type == "error" {
				failure = bridgeFailure(event.Code)
			} else if event.Type != "prepared" {
				failure = fmt.Errorf("claudesdk: SDK preparation receipt is missing")
			}
		} else {
			failure = fmt.Errorf("claudesdk: SDK preparation receipt is missing")
		}
	}
	if failure != nil {
		p.session.process.Cancel()
		return nil, failure
	}
	close(p.ready)
	select {
	case <-p.started:
	case <-p.session.process.Context().Done():
	case <-p.session.process.Done():
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.binding != nil {
		return p.binding, nil
	}
	p.closed = true
	return nil, fmt.Errorf("claudesdk: preparation ended before start")
}
