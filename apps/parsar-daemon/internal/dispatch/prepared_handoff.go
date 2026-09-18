package dispatch

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

type preparedHandoffPhase uint8

const (
	preparedHandoffStarting preparedHandoffPhase = iota
	preparedHandoffPublishing
	preparedHandoffActive
	preparedHandoffStopping
	preparedHandoffSettled
)

type preparedReleasePhase uint8

const (
	preparedReleaseIdle preparedReleasePhase = iota
	preparedReleaseRequested
	preparedReleaseRunning
	preparedReleaseNativeDone
	preparedReleaseDone
)

type preparedReleaseTarget uint8

const (
	preparedReleaseNone preparedReleaseTarget = iota
	preparedReleasePreparation
	preparedReleaseSession
	preparedReleaseClose
)

type preparedReleaseCause uint16

const (
	preparedReleaseCompletion preparedReleaseCause = 1 << iota
	preparedReleasePromptCancel
	preparedReleaseStartFailure
	preparedReleaseStatusFailure
	preparedReleaseSenderFailure
	preparedReleaseRouterShutdown
	preparedReleaseDeviceShutdown
	preparedReleaseOutputClosed
)

const preparedAbortStartCauses = preparedReleasePromptCancel |
	preparedReleaseStartFailure |
	preparedReleaseStatusFailure |
	preparedReleaseSenderFailure |
	preparedReleaseRouterShutdown |
	preparedReleaseDeviceShutdown

// preparedHandoff is the one lifecycle owner between Prepared.Start admission
// and final output cleanup. Router.mu protects every mutable field. The three
// channels are closed once to publish release request, native settlement, and
// complete output/release settlement respectively.
type preparedHandoff struct {
	preparation *preparationState
	prepared    agent.Prepared
	phase       preparedHandoffPhase
	session     agent.Session

	release       preparedReleasePhase
	releaseTarget preparedReleaseTarget
	releaseCauses preparedReleaseCause
	nativeDone    chan struct{}
	releaseDone   chan struct{}
	nativeErr     error
	releaseErr    error
	outcome       *proto.DonePayload

	terminal   *proto.Envelope
	outputDone chan struct{}
	outputErr  error
}

type preparedReleaseAction struct {
	state    *sessionState
	handoff  *preparedHandoff
	target   preparedReleaseTarget
	prepared agent.PreparedCancellation
	session  agent.Session
}

func newPreparedHandoff(p *preparationState) *preparedHandoff {
	return &preparedHandoff{
		preparation: p,
		prepared:    p.prepared,
		phase:       preparedHandoffStarting,
		nativeDone:  make(chan struct{}),
		releaseDone: make(chan struct{}),
		outputDone:  make(chan struct{}),
	}
}

func (r *Router) requestPreparedReleaseLocked(state *sessionState, cause preparedReleaseCause) {
	handoff := state.preparedHandoff
	if handoff == nil || handoff.release == preparedReleaseDone {
		return
	}
	handoff.releaseCauses |= cause
	if handoff.release == preparedReleaseIdle {
		handoff.release = preparedReleaseRequested
		state.retain = false
		state.steeringClosed = true
		r.clearInteractionRoutesLocked(state)
	}
	if cause&preparedAbortStartCauses != 0 {
		state.ctxCancel()
	}
	r.maybeStartPreparedReleaseLocked(state)
}

func (r *Router) maybeStartPreparedReleaseLocked(state *sessionState) {
	handoff := state.preparedHandoff
	if handoff == nil || handoff.release != preparedReleaseRequested {
		return
	}
	action := preparedReleaseAction{state: state, handoff: handoff}
	switch {
	case handoff.phase == preparedHandoffStarting:
		// Natural completion may be observed before Start returns. It must not
		// turn successful transfer into cancellation; wait for the Session.
		if handoff.releaseCauses&preparedAbortStartCauses == 0 {
			return
		}
		prepared, ok := handoff.prepared.(agent.PreparedCancellation)
		if !ok {
			return
		}
		action.target, action.prepared = preparedReleasePreparation, prepared
	case handoff.session != nil:
		action.target, action.session = preparedReleaseSession, handoff.session
	case handoff.phase == preparedHandoffStopping:
		action.target = preparedReleaseClose
	default:
		return
	}
	handoff.release = preparedReleaseRunning
	handoff.releaseTarget = action.target
	go r.runPreparedRelease(action)
}

func (r *Router) runPreparedRelease(action preparedReleaseAction) {
	receiptErr := r.finishSteering(action.state)
	var releaseErr error
	var outcome *proto.DonePayload
	switch action.target {
	case preparedReleasePreparation:
		ctx, cancel := context.WithTimeout(context.Background(), preparedCancelTimeout)
		releaseErr = action.prepared.Cancel(ctx)
		cancel()
		observed := action.prepared.CancellationOutcome()
		outcome = &observed
	case preparedReleaseSession:
		releaseErr = action.session.Cancel(context.Background())
		if provider, ok := action.session.(cancellationOutcomeProvider); ok {
			observed := provider.CancellationOutcome()
			outcome = &observed
		}
	case preparedReleaseClose:
		releaseErr = r.closePreparationResource(action.handoff.preparation)
	}
	action.state.ctxCancel()
	r.mu.Lock()
	action.handoff.nativeErr = errors.Join(receiptErr, releaseErr)
	action.handoff.outcome = outcome
	action.handoff.release = preparedReleaseNativeDone
	close(action.handoff.nativeDone)
	r.mu.Unlock()
}

func (r *Router) observePreparedOutput(state *sessionState, env proto.Envelope) (hold, drop bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	handoff := state.preparedHandoff
	if handoff == nil {
		return false, false
	}
	if handoff.terminal != nil {
		return false, true
	}
	if env.Type != proto.TypeDone {
		return false, false
	}
	terminal := env
	handoff.terminal = &terminal
	r.requestPreparedReleaseLocked(state, preparedReleaseCompletion)
	return true, false
}

func (r *Router) finishPreparedOutput(state *sessionState, err error) {
	r.mu.Lock()
	handoff := state.preparedHandoff
	if handoff != nil {
		handoff.outputErr = err
		r.requestPreparedReleaseLocked(state, preparedReleaseOutputClosed)
		close(handoff.outputDone)
	}
	r.mu.Unlock()
}

func (r *Router) forwardPreparedOutput(state *sessionState) {
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
				r.finishPreparedOutput(state, nil)
				return
			}
			hold, drop := r.observePreparedOutput(state, env)
			if hold || drop {
				continue
			}
			switch env.Type {
			case proto.TypePermissionRequest, proto.TypePermissionCancel, proto.TypePromptForUserChoice:
				r.indexPermissionFrame(state, env)
			}
			if err := r.sendSessionOutput(pumpCtx, state, env); err != nil {
				r.mu.Lock()
				r.requestPreparedReleaseLocked(state, preparedReleaseSenderFailure)
				r.mu.Unlock()
				r.drain(state.out)
				r.finishPreparedOutput(state, err)
				return
			}
		case <-r.shutdownCh:
			r.mu.Lock()
			r.requestPreparedReleaseLocked(state, preparedReleaseRouterShutdown)
			r.mu.Unlock()
			r.drain(state.out)
			r.finishPreparedOutput(state, ErrRouterClosed)
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

func (r *Router) awaitPreparedRelease(ctx context.Context, handoff *preparedHandoff) error {
	select {
	case <-handoff.releaseDone:
		r.mu.Lock()
		err := errors.Join(handoff.releaseErr, handoff.outputErr)
		r.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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

func (r *Router) settlePreparedHandoff(state *sessionState) {
	handoff := state.preparedHandoff
	<-handoff.nativeDone
	<-handoff.outputDone

	r.mu.Lock()
	closePreparation := handoff.preparation.owns && handoff.releaseTarget != preparedReleaseClose
	r.mu.Unlock()
	var closeErr error
	if closePreparation {
		closeErr = r.closePreparationResource(handoff.preparation)
	}

	r.mu.Lock()
	releaseErr := errors.Join(handoff.nativeErr, closeErr)
	outputErr := handoff.outputErr
	terminal := handoff.terminal
	causes := handoff.releaseCauses
	handoff.phase = preparedHandoffSettled
	state.session = nil
	r.clearInteractionRoutesLocked(state)
	r.mu.Unlock()

	var terminalErr error
	if outputErr == nil && causes&preparedReleaseRouterShutdown == 0 {
		failure := ""
		synthesize := false
		switch {
		case releaseErr != nil:
			failure = "failed to release completed executor"
		case causes&preparedReleaseStartFailure != 0 && causes&preparedReleasePromptCancel == 0:
			failure, synthesize = "prepared execution could not start", terminal == nil
		}
		terminalErr = r.forwardPreparedTerminal(state, failure, terminal, synthesize)
	}
	r.cleanupSession(state)

	r.mu.Lock()
	handoff.releaseErr = releaseErr
	handoff.outputErr = errors.Join(handoff.outputErr, terminalErr)
	handoff.release = preparedReleaseDone
	close(handoff.releaseDone)
	r.mu.Unlock()
}
