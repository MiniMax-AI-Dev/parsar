package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type shutdownAttempt struct {
	done chan struct{}
	err  error
}

// Shutdown stops admission and waits for owned cleanup. A later call retries
// only failed cleanup; caller timeouts never abandon or duplicate in-flight work.
func (r *Router) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if previous := r.shutdownAttempt; previous != nil {
		select {
		case <-previous.done:
			if previous.err == nil {
				r.mu.Unlock()
				return nil
			}
		default:
			r.mu.Unlock()
			return waitShutdown(ctx, previous)
		}
	}

	first := !r.closed
	var victims []sessionCancellation
	for _, state := range r.sessions {
		state.retain = false
		if state.preparedHandoff != nil {
			victims = append(victims, r.sessionCancellationLocked(state, true))
		} else if first {
			victims = append(victims, sessionCancellation{runID: state.runID, ctxCancel: state.ctxCancel, session: state.session})
		}
	}
	if first {
		r.closed = true
		for _, states := range r.idle {
			for state := range states {
				state.retain = false
				if state.idleTimer != nil {
					state.idleTimer.Stop()
				}
				victims = append(victims, sessionCancellation{runID: state.runID, ctxCancel: state.ctxCancel, session: state.session})
			}
		}
		r.idle = make(map[string]map[*sessionState]struct{})
		// Prepared release claims exist before this signal can interrupt output.
		close(r.shutdownCh)
	}
	preparations := r.closePendingPreparationsLocked()
	attempt := &shutdownAttempt{done: make(chan struct{})}
	r.shutdownAttempt = attempt
	// Keep the WaitGroup non-zero until all cancellation dispatch is complete.
	r.shutdownWG.Add(1)
	r.mu.Unlock()

	for _, p := range preparations {
		go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
	}
	go r.runShutdownAttempt(attempt, victims)
	return waitShutdown(ctx, attempt)
}

func (r *Router) runShutdownAttempt(attempt *shutdownAttempt, victims []sessionCancellation) {
	var releaseErr error
	for _, victim := range victims {
		if victim.handoff != nil {
			// The cleanup operation has its own fixed native deadline. The
			// Shutdown caller's deadline only bounds its wait for this attempt.
			if err := r.awaitPreparedNativeRelease(context.Background(), victim.release, victim.attempt); err != nil {
				releaseErr = errors.Join(releaseErr, fmt.Errorf("dispatch: prepared run %s: %w", victim.runID, err))
			}
			continue
		}
		victim.ctxCancel()
		if victim.session != nil {
			if err := victim.session.Cancel(context.Background()); err != nil {
				r.log.Warn("session.Cancel failed", "run_id", victim.runID, "err", err)
			}
		}
	}
	r.shutdownWG.Done()
	if releaseErr != nil {
		// A failed prepared release may leave its output consumer blocked. Do
		// not wait for all workers; retain the exact target for the next call.
		r.finishShutdownAttempt(attempt, releaseErr)
		return
	}

	r.shutdownWG.Wait()
	r.mu.Lock()
	if r.workspaceWrite != nil && r.workspaceWrite.uncertain {
		attempt.err = errors.Join(attempt.err, errors.New("dispatch: local workspace write remains uncertain"))
	}
	for _, p := range r.preparations {
		if p.owns {
			cause := p.closeErr
			if cause == nil {
				cause = errors.New("cleanup has not settled")
			}
			attempt.err = errors.Join(attempt.err, fmt.Errorf("dispatch: preparation %s: %w", p.status.Handle, cause))
		}
	}
	close(attempt.done)
	r.mu.Unlock()
}

func (r *Router) finishShutdownAttempt(attempt *shutdownAttempt, err error) {
	r.mu.Lock()
	attempt.err = errors.Join(attempt.err, err)
	close(attempt.done)
	r.mu.Unlock()
}

func waitShutdown(ctx context.Context, attempt *shutdownAttempt) error {
	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Router) handleDeviceShutdown(ctx context.Context, env proto.Envelope) error {
	var payload proto.DeviceShutdownPayload
	_ = env.DecodePayload(&payload) // body optional
	r.log.InfoContext(ctx, "device_shutdown received, cancelling runs", "reason", payload.Reason, "active_runs", r.ActiveRuns())
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	victims := make([]sessionCancellation, 0, len(r.sessions))
	for _, state := range r.sessions {
		state.retain = false
		victims = append(victims, r.sessionCancellationLocked(state, true))
	}
	for _, states := range r.idle {
		for state := range states {
			state.retain = false
			if state.idleTimer != nil {
				state.idleTimer.Stop()
			}
			victims = append(victims, sessionCancellation{runID: state.runID, ctxCancel: state.ctxCancel, session: state.session})
		}
	}
	r.idle = make(map[string]map[*sessionState]struct{})
	preparations := r.closePendingPreparationsLocked()
	r.mu.Unlock()
	for _, p := range preparations {
		go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
	}
	r.cancelSessions(ctx, victims)
	return nil
}

type sessionCancellation struct {
	runID     string
	ctxCancel context.CancelFunc
	session   agent.Session
	handoff   *preparedHandoff
	release   *preparedRelease
	attempt   *preparedReleaseAttempt
}

func (r *Router) sessionCancellationLocked(state *sessionState, retry bool) sessionCancellation {
	if state.preparedHandoff != nil {
		release, attempt := r.claimPreparedReleaseLocked(state, true, "", retry)
		return sessionCancellation{runID: state.runID, handoff: state.preparedHandoff, release: release, attempt: attempt}
	}
	return sessionCancellation{runID: state.runID, ctxCancel: state.ctxCancel, session: state.session}
}

func (r *Router) cancelSessions(ctx context.Context, sessions []sessionCancellation) {
	for _, session := range sessions {
		if session.handoff != nil {
			if err := r.awaitPreparedRelease(ctx, session.handoff, session.release, session.attempt); err != nil {
				r.log.Warn("prepared session release failed", "run_id", session.runID, "err", err)
			}
			continue
		}
		session.ctxCancel()
		if session.session == nil {
			continue
		}
		if err := session.session.Cancel(ctx); err != nil {
			r.log.Warn("session.Cancel failed", "run_id", session.runID, "err", err)
		}
	}
}
