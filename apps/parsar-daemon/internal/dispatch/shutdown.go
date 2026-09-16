package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type shutdownAttempt struct {
	done chan struct{}
	err  error
}

// Shutdown stops admission and waits for owned cleanup; failed preparation cleanup can be retried.
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
	var victims []sessionCancellation
	if !r.closed {
		r.closed = true
		close(r.shutdownCh)
		for _, s := range r.sessions {
			s.retain = false
			victims = append(victims, sessionCancellation{s.runID, s.ctxCancel, s.session})
		}
		for _, states := range r.idle {
			for s := range states {
				s.retain = false
				if s.idleTimer != nil {
					s.idleTimer.Stop()
				}
				victims = append(victims, sessionCancellation{s.runID, s.ctxCancel, s.session})
			}
		}
		r.idle = make(map[string]map[*sessionState]struct{})
	}
	preparations := r.closePendingPreparationsLocked()
	attempt := &shutdownAttempt{done: make(chan struct{})}
	r.shutdownAttempt = attempt
	// Keep the wait group active until cancellation dispatch has finished.
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		for _, p := range preparations {
			go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
		}
		cancelSessions(ctx, victims, r.log)
		r.shutdownWG.Done()
		r.shutdownWG.Wait()
		r.mu.Lock()
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
	}()
	return waitShutdown(ctx, attempt)
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
	// Snapshot under lock, cancel outside so a slow Session.Cancel
	// can't stall other Handle calls.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	preparations := r.closePendingPreparationsLocked()
	victims := make([]sessionCancellation, 0, len(r.sessions))
	for _, s := range r.sessions {
		s.retain = false
		victims = append(victims, sessionCancellation{s.runID, s.ctxCancel, s.session})
	}
	for _, states := range r.idle {
		for s := range states {
			s.retain = false
			if s.idleTimer != nil {
				s.idleTimer.Stop()
			}
			victims = append(victims, sessionCancellation{s.runID, s.ctxCancel, s.session})
		}
	}
	r.idle = make(map[string]map[*sessionState]struct{})
	r.mu.Unlock()
	for _, p := range preparations {
		go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
	}
	cancelSessions(ctx, victims, r.log)
	return nil
}

// cancelSessions cancels ctx AND invokes Session.Cancel so agents that
// don't watch ctx (subprocess wrappers relying on SIGTERM via Cancel)
// actually start their shutdown — otherwise the pump's drain blocks
// forever waiting for an out channel nobody closes.
type sessionCancellation struct {
	runID     string
	ctxCancel context.CancelFunc
	session   agent.Session
}

func cancelSessions(ctx context.Context, sessions []sessionCancellation, log *slog.Logger) {
	for _, s := range sessions {
		s.ctxCancel()
		if s.session == nil {
			continue
		}
		if err := s.session.Cancel(ctx); err != nil {
			log.Warn("session.Cancel failed", "run_id", s.runID, "err", err)
		}
	}
}
