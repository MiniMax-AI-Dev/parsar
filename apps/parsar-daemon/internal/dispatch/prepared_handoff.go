package dispatch

import (
	"context"
	"errors"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

var errPreparedStatusDelivery = errors.New("prepared execution status delivery failed")

// preparedReleaseAttempt is one serialized call to the handoff's fixed native
// cancellation target. Router.mu protects err until done closes.
type preparedReleaseAttempt struct {
	done chan struct{}
	err  error
}

// preparedRelease is the permanent terminal claim for a handoff. A failed
// attempt may be retried explicitly, but the target and claim never change.
// Router.mu protects its fields.
type preparedRelease struct {
	abort     chan struct{}
	failure   string
	attempt   *preparedReleaseAttempt
	succeeded bool
	outcome   *proto.DonePayload
	settled   chan struct{}
}

func (release *preparedRelease) aborted() bool {
	select {
	case <-release.abort:
		return true
	default:
		return false
	}
}

// preparedHandoff owns the exact PreparedCancellation from Start admission
// through native cleanup and output settlement. It never falls back to the
// returned Session or Prepared.Close.
type preparedHandoff struct {
	preparation *preparationState
	target      agent.PreparedCancellation

	startDone   chan struct{}
	outputReady chan struct{}
	outputDone  chan struct{}

	// Every admitted native mutation holds a read lock through its receipt.
	// A release attempt takes the write lock before native cancellation.
	operations sync.RWMutex

	published           bool // started status committed; protected by Router.mu
	release             *preparedRelease
	terminal            *proto.Envelope
	outputErr           error
	shutdownInterrupted bool
}

func newPreparedHandoff(p *preparationState, target agent.PreparedCancellation) *preparedHandoff {
	return &preparedHandoff{
		preparation: p,
		target:      target,
		startDone:   make(chan struct{}),
		outputReady: make(chan struct{}),
		outputDone:  make(chan struct{}),
	}
}

// preparedOperationLocked admits one mutation at the same linearization point
// used by release. The returned function must run after the native call, replay
// bookkeeping and receipt send have all finished. Router.mu must be held.
func (r *Router) preparedOperationLocked(state *sessionState) (agent.Session, func(), bool) {
	if state == nil || state.session == nil || state.steeringClosed {
		return nil, nil, false
	}
	handoff := state.preparedHandoff
	if handoff == nil {
		return state.session, func() {}, true
	}
	if handoff.release != nil {
		return nil, nil, false
	}
	handoff.operations.RLock()
	return state.session, handoff.operations.RUnlock, true
}

func (r *Router) interactionRouteOpenLocked(state *sessionState) bool {
	if state == nil || r.closed || state.ctx.Err() != nil || state.steeringClosed {
		return false
	}
	if handoff := state.preparedHandoff; handoff != nil {
		return handoff.release == nil
	}
	return state.session != nil
}

// claimPreparedReleaseLocked closes admission permanently and returns the
// current native attempt. retry starts a new serialized attempt only after the
// previous one failed. Router.mu must be held.
func (r *Router) claimPreparedReleaseLocked(state *sessionState, abort bool, failure string, retry bool) (*preparedRelease, *preparedReleaseAttempt) {
	handoff := state.preparedHandoff
	release := handoff.release
	if release == nil {
		release = &preparedRelease{abort: make(chan struct{}), failure: failure, settled: make(chan struct{})}
		handoff.release = release
		state.retain = false
		state.steeringClosed = true
		state.session = nil
		r.clearInteractionRoutesLocked(state)
	}
	if abort && !release.aborted() {
		close(release.abort)
	}
	if release.succeeded {
		return release, release.attempt
	} else if current := release.attempt; current != nil {
		select {
		case <-current.done:
			if current.err == nil || !retry {
				return release, current
			}
		default:
			return release, current
		}
	}

	attempt := &preparedReleaseAttempt{done: make(chan struct{})}
	release.attempt = attempt
	p := handoff.preparation
	p.busy = true
	p.closeErr = nil
	r.shutdownWG.Add(1)
	go r.runPreparedRelease(state, handoff, release, attempt)
	return release, attempt
}

func (r *Router) runPreparedRelease(state *sessionState, handoff *preparedHandoff, release *preparedRelease, attempt *preparedReleaseAttempt) {
	defer r.shutdownWG.Done()

	// Natural completion must publish started before it can release the native
	// resource. Abort is allowed to fence a Start that is still in progress.
	select {
	case <-handoff.startDone:
	case <-release.abort:
	}
	handoff.operations.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), preparedCancelTimeout)
	nativeErr := handoff.target.Cancel(ctx)
	cancel()
	var outcome *proto.DonePayload
	if nativeErr == nil {
		observed := handoff.target.CancellationOutcome()
		outcome = &observed
	}
	handoff.operations.Unlock()

	r.mu.Lock()
	attempt.err = nativeErr
	if nativeErr != nil {
		handoff.preparation.busy = false
		handoff.preparation.closeErr = nativeErr
		close(attempt.done)
		r.mu.Unlock()
		return
	}
	release.succeeded = true
	release.outcome = outcome
	close(attempt.done)
	r.mu.Unlock()

	// Cancel success promises local cleanup and no further output writes. Wait
	// for Start and the one output consumer before publishing terminal state.
	<-handoff.startDone
	<-handoff.outputDone

	r.mu.Lock()
	outputErr := handoff.outputErr
	terminal := handoff.terminal
	closed := r.closed
	r.mu.Unlock()

	var terminalErr error
	if outputErr == nil && !closed {
		terminalErr = r.forwardPreparedTerminal(state, release.failure, terminal, release.failure != "" && terminal == nil)
	}
	r.cleanupSession(state)

	r.mu.Lock()
	handoff.outputErr = errors.Join(handoff.outputErr, terminalErr)
	p := handoff.preparation
	p.busy = false
	p.closeErr = terminalErr
	p.prepared = nil
	p.owns = false
	p.handoff = nil
	p.cancel()
	close(release.settled)
	r.mu.Unlock()
}

func (r *Router) forwardPreparedOutput(state *sessionState) {
	defer r.shutdownWG.Done()
	handoff := state.preparedHandoff
	defer close(handoff.outputDone)
	close(handoff.outputReady)

	pumpCtx := context.Background()
	if state.traceparent != "" {
		if carrier, err := obslog.ParseTraceparent(state.traceparent); err == nil {
			pumpCtx = obslog.WithTrace(pumpCtx, carrier)
		}
	}
	r.log.InfoContext(pumpCtx, "pump: started", "run_id", state.runID)
	for {
		select {
		case env, ok := <-state.out:
			if !ok {
				r.log.InfoContext(pumpCtx, "pump: out channel closed", "run_id", state.runID)
				r.mu.Lock()
				if handoff.release == nil {
					r.claimPreparedReleaseLocked(state, false, "", false)
				}
				r.mu.Unlock()
				return
			}
			r.mu.Lock()
			if handoff.terminal != nil {
				r.mu.Unlock()
				continue
			}
			if env.Type == proto.TypeDone {
				terminal := env
				handoff.terminal = &terminal
				r.claimPreparedReleaseLocked(state, false, "", false)
				r.mu.Unlock()
				continue
			}
			drainOnly := handoff.outputErr != nil || handoff.shutdownInterrupted || r.closed
			r.mu.Unlock()
			if drainOnly {
				continue
			}
			switch env.Type {
			case proto.TypePermissionRequest, proto.TypePermissionCancel, proto.TypePromptForUserChoice:
				r.indexPermissionFrame(state, env)
			}
			if err := r.sendSessionOutput(pumpCtx, state, env); err != nil {
				r.mu.Lock()
				if r.closed {
					handoff.shutdownInterrupted = true
				} else {
					handoff.outputErr = errors.Join(handoff.outputErr, err)
				}
				r.claimPreparedReleaseLocked(state, true, "", false)
				r.mu.Unlock()
				r.drain(state.out)
				return
			}
		case <-r.shutdownCh:
			r.mu.Lock()
			handoff.shutdownInterrupted = true
			r.claimPreparedReleaseLocked(state, true, "", false)
			r.mu.Unlock()
			r.drain(state.out)
			return
		}
	}
}

func (r *Router) sendSessionOutput(pumpCtx context.Context, state *sessionState, env proto.Envelope) error {
	if env.Trace == "" && state.traceparent != "" {
		env.Trace = state.traceparent
	}
	r.log.InfoContext(pumpCtx, "pump: forwarding envelope", "run_id", state.runID, "type", env.Type, "env_id", env.ID)
	sendCtx, cancel := context.WithCancel(context.Background())
	stopOnShutdown := make(chan struct{})
	go func() {
		select {
		case <-r.shutdownCh:
			cancel()
		case <-stopOnShutdown:
		}
	}()
	err := r.sender.Send(sendCtx, env)
	close(stopOnShutdown)
	cancel()
	if err != nil {
		r.log.ErrorContext(pumpCtx, "send envelope failed", "type", env.Type, "run_id", env.ID, "err", err)
	}
	return err
}

func (r *Router) forwardPreparedTerminal(state *sessionState, failure string, terminal *proto.Envelope, synthesize bool) error {
	pumpCtx := context.Background()
	if state.traceparent != "" {
		if carrier, err := obslog.ParseTraceparent(state.traceparent); err == nil {
			pumpCtx = obslog.WithTrace(pumpCtx, carrier)
		}
	}
	if failure != "" {
		errEnv, err := proto.NewEnvelopeWithTrace(proto.TypeError, state.runID, proto.ErrorPayload{Error: failure}, state.traceparent)
		if err != nil {
			return err
		}
		if err := r.sendSessionOutput(pumpCtx, state, errEnv); err != nil {
			return err
		}
	}
	if terminal != nil {
		return r.sendSessionOutput(pumpCtx, state, *terminal)
	}
	if !synthesize {
		return nil
	}
	done, err := proto.NewEnvelopeWithTrace(proto.TypeDone, state.runID, proto.DonePayload{}, state.traceparent)
	if err != nil {
		return err
	}
	return r.sendSessionOutput(pumpCtx, state, done)
}

func (r *Router) awaitPreparedRelease(ctx context.Context, handoff *preparedHandoff, release *preparedRelease, attempt *preparedReleaseAttempt) error {
	if err := r.awaitPreparedNativeRelease(ctx, release, attempt); err != nil {
		return err
	}
	r.mu.Lock()
	err := handoff.outputErr
	r.mu.Unlock()
	return err
}

func (r *Router) awaitPreparedNativeRelease(ctx context.Context, release *preparedRelease, attempt *preparedReleaseAttempt) error {
	select {
	case <-attempt.done:
		r.mu.Lock()
		err := attempt.err
		r.mu.Unlock()
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-release.settled:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
