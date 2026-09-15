package dispatch

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// pump forwards every Envelope the session writes onto out to the
// upstream sender, then cleans up when the session closes out.
func (r *Router) pump(s *sessionState) {
	defer r.shutdownWG.Done()
	defer r.cleanupSession(s)
	_ = r.forwardSessionOutput(s, true)
}

// wait is false only after a failed Start and its cancellation have returned:
// no Session owns the output sink, so only already queued frames can be read.
func (r *Router) forwardSessionOutput(s *sessionState, wait bool) error {
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
		if !wait && len(s.out) == 0 {
			return nil
		}
		select {
		case env, ok := <-s.out:
			if !ok {
				r.log.InfoContext(pumpCtx, "pump: out channel closed", "run_id", s.runID)
				return nil
			}
			if s.session != nil {
				r.indexPermissionFrame(s, env)
			}
			if env.Type == proto.TypeDone && s.releaseOnCompletion {
				if err := r.releaseCompletedSession(s); err != nil {
					sendCtx, stop := r.shutdownContext(pumpCtx)
					r.emitTerminalError(sendCtx, s.runID, "failed to release completed executor")
					stop()
					continue
				}
			}
			r.log.InfoContext(pumpCtx, "pump: forwarding envelope", "run_id", s.runID, "type", env.Type, "env_id", env.ID)
			// Stamp the run's trace onto outbound frames so the
			// gateway attributes daemon-emitted lines to the same
			// trace_id as the original prompt_request.
			if env.Trace == "" && s.traceparent != "" {
				env.Trace = s.traceparent
			}
			// Short-ish send ctx that respects router shutdown — if
			// the transport is wedged we don't want to block forever.
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
				// Sender failed — log, ask the session to wind down,
				// but KEEP draining out so the agent's goroutines
				// don't block on a full channel.
				r.log.ErrorContext(pumpCtx, "send envelope failed", "type", env.Type, "run_id", env.ID, "err", err)
				r.mu.Lock()
				s.retain = false
				r.mu.Unlock()
				s.ctxCancel()
				if wait {
					r.drain(s.out)
				}
				return err
			}
		case <-r.shutdownCh:
			// Router shutdown — cancel + drain so the session's
			// goroutines unblock and close out cleanly.
			r.mu.Lock()
			s.retain = false
			r.mu.Unlock()
			s.ctxCancel()
			if wait {
				r.drain(s.out)
			}
			return ErrRouterClosed
		}
	}
}
