package dispatch

import (
	"context"
	"log/slog"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Shutdown cancels every active session and waits for pumps to drain.
// Idempotent. Cancels ctx AND calls Session.Cancel — agents that
// don't watch ctx (e.g. subprocess wrappers that need SIGTERM via
// Cancel) leave their out channel open otherwise and the pump's
// drain blocks forever.
func (r *Router) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.shutdownCh)
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

	done := make(chan struct{})
	go func() {
		r.shutdownWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
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
