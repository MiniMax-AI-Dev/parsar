package claudesdk

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Cancel confirms process exit and output drain, independently of terminal delivery.
func (s *session) Cancel(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-s.settled:
		return nil
	default:
	}
	s.process.Cancel()
	select {
	case <-s.settled:
		return nil
	case <-ctx.Done():
		select {
		case <-s.settled:
			return nil
		default:
			return ctx.Err()
		}
	}
}

// CancellationOutcome is available after successful Cancel or terminal publication.
// Closing settled publishes the immutable snapshot; an unsettled result is unknown.
func (s *session) CancellationOutcome() proto.DonePayload {
	select {
	case <-s.settled:
		return s.outcome
	default:
		return proto.DonePayload{}
	}
}
