package dispatch

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// pump forwards every Envelope the session writes onto out to the
// upstream sender, then cleans up when the session closes out.
func (r *Router) pump(s *sessionState) {
	defer r.shutdownWG.Done()
	defer r.cleanupSession(s)
	_ = r.forwardSessionOutput(s)
}

func (r *Router) forwardSessionOutput(s *sessionState) error {
	// Logging-only ctx carrying the run's trace; sends use their own
	// ctx tied to shutdownCh.
	pumpCtx := context.Background()
	if s.traceparent != "" {
		if carrier, err := obslog.ParseTraceparent(s.traceparent); err == nil {
			pumpCtx = obslog.WithTrace(pumpCtx, carrier)
		}
	}
	r.log.InfoContext(pumpCtx, "pump: started", "run_id", s.runID)

	// Long-lived send ctx — must keep forwarding even after the
	// session's ctx is cancelled (session might emit a final "done"
	// in response to cancel). Stops on out close or router shutdown.
	for {
		select {
		case env, ok := <-s.out:
			if !ok {
				r.log.InfoContext(pumpCtx, "pump: out channel closed", "run_id", s.runID)
				return nil
			}
			hold, drop := r.observeOutputCompletion(s, env)
			if hold || drop {
				continue
			}
			switch env.Type {
			case proto.TypePermissionRequest, proto.TypePermissionCancel, proto.TypePromptForUserChoice:
				r.indexPermissionFrame(s, env)
			}
			if env.Type == proto.TypeDone {
				_, releaseErr := r.releaseObservedCompletion(s)
				if releaseErr != nil {
					errEnv, err := proto.NewEnvelopeWithTrace(proto.TypeError, s.runID, proto.ErrorPayload{Error: "failed to release completed executor"}, s.traceparent)
					if err != nil {
						return errors.Join(releaseErr, err)
					}
					if err := r.sendSessionOutput(pumpCtx, s, errEnv); err != nil {
						r.abortSessionOutput(s)
						return errors.Join(releaseErr, err)
					}
				}
			}
			r.log.InfoContext(pumpCtx, "pump: forwarding envelope", "run_id", s.runID, "type", env.Type, "env_id", env.ID)
			err := r.sendSessionOutput(pumpCtx, s, env)
			if err != nil {
				// Sender failed — log, ask the session to wind down,
				// but KEEP draining out so the agent's goroutines
				// don't block on a full channel.
				r.abortSessionOutput(s)
				return err
			}
		case <-r.shutdownCh:
			// Router shutdown — cancel + drain so the session's
			// goroutines unblock and close out cleanly.
			r.mu.Lock()
			s.retain = false
			r.mu.Unlock()
			s.ctxCancel()
			r.drain(s.out)
			return ErrRouterClosed
		}
	}
}

// observeOutputCompletion holds the one terminal frame while Prepared.Start is
// settling. The consumer keeps draining so a native writer cannot block on the
// bounded channel. Frames after Done are invalid terminal output and are dropped.
func (r *Router) observeOutputCompletion(s *sessionState, env proto.Envelope) (hold, drop bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.completionObserved {
		return false, true
	}
	if env.Type != proto.TypeDone {
		return false, false
	}
	s.completionObserved = true
	if s.deferCompletion {
		completion := env
		s.deferredCompletion = &completion
		return true, false
	}
	return false, false
}

// releaseObservedCompletion releases a published Session exactly once after
// its terminal frame has been observed.
func (r *Router) releaseObservedCompletion(s *sessionState) (bool, error) {
	r.mu.Lock()
	observed := s.completionObserved
	r.mu.Unlock()
	if !observed {
		return false, nil
	}
	return r.releaseCompletedSession(s)
}

func (r *Router) sendSessionOutput(pumpCtx context.Context, s *sessionState, env proto.Envelope) error {
	if env.Trace == "" && s.traceparent != "" {
		env.Trace = s.traceparent
	}
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

func (r *Router) abortSessionOutput(s *sessionState) {
	r.mu.Lock()
	s.retain = false
	r.mu.Unlock()
	s.ctxCancel()
	r.drain(s.out)
}

// forwardDeferredCompletion sends the held Done after Start settlement and any
// required native release. A failure is sent first so Gateway never closes the
// subscription before observing it.
func (r *Router) forwardDeferredCompletion(s *sessionState, failure string) (bool, error) {
	r.mu.Lock()
	completion := s.deferredCompletion
	s.deferredCompletion = nil
	r.mu.Unlock()
	if completion == nil {
		return false, nil
	}
	pumpCtx := context.Background()
	if s.traceparent != "" {
		if carrier, err := obslog.ParseTraceparent(s.traceparent); err == nil {
			pumpCtx = obslog.WithTrace(pumpCtx, carrier)
		}
	}
	if failure != "" {
		errEnv, err := proto.NewEnvelopeWithTrace(proto.TypeError, s.runID, proto.ErrorPayload{Error: failure}, s.traceparent)
		if err != nil {
			return true, err
		}
		if err := r.sendSessionOutput(pumpCtx, s, errEnv); err != nil {
			return true, err
		}
	}
	return true, r.sendSessionOutput(pumpCtx, s, *completion)
}
