package codex

import (
	"context"
	"time"
)

func (s *Session) Cancel(_ context.Context) error {
	s.cancelled.Store(true)
	s.cancelOnce.Do(func() {
		turnID, active := s.stopSteering()
		s.stopCodexInteractionTimers()
		// Best effort: a known Turn must use its native identity. An explicit
		// empty ID invokes native startup cancellation before turn/started.
		if threadID := s.currentThreadID(); threadID != "" && active {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = s.rpc.request(ctx, "turn/interrupt", TurnInterruptParams{ThreadID: threadID, TurnID: turnID}, func(frame any) error {
				return s.rpc.writeFrameContext(ctx, frame)
			})
		}
		s.cancelFn()
		_ = s.rpc.Close()
	})
	return nil
}
