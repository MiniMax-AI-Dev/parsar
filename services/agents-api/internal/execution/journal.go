package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type journal struct {
	store                 *store.Store
	tenant, session, turn string
	next                  int32
	batch                 []store.ExecutionEvent
	bytes                 int
}

func recordCancellation(ctx context.Context, journal *journal, reply cancellationResult) error {
	if reply.err != nil {
		return nil
	}
	env, err := proto.NewEnvelope("cancel_receipt", journal.turn, reply.ack)
	if err != nil {
		return err
	}
	return journal.observe(ctx, env)
}

func (j *journal) observe(ctx context.Context, env proto.Envelope) error {
	switch env.Type {
	case proto.TypeDelta, proto.TypeThinking, proto.TypeToolCall, proto.TypeUsage,
		proto.TypeError, proto.TypeDone, proto.TypePromptSteerAck, "cancel_receipt":
	default:
		return nil
	}
	if len(env.Payload) > 512*1024 {
		return store.ErrEventLimit
	}
	if j.bytes+len(env.Payload) > 768*1024 || len(j.batch) >= 64 {
		if err := j.flush(ctx); err != nil {
			return err
		}
	}
	j.batch = append(j.batch, store.ExecutionEvent{Kind: env.Type, Payload: env.Payload})
	j.bytes += len(env.Payload)
	return nil
}

func (j *journal) flush(ctx context.Context) error {
	if len(j.batch) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := j.store.AppendTurnEvents(ctx, j.tenant, j.session, j.turn, j.next, j.batch); err != nil {
		return err
	}
	j.next += int32(len(j.batch))
	j.batch, j.bytes = nil, 0
	return nil
}

// Cancellation receipts use a separate waiter; preceding frames can still be queued.
func (j *journal) drain(ctx context.Context, upstream <-chan proto.Envelope) error {
	for range 256 {
		select {
		case env, ok := <-upstream:
			if !ok {
				return nil
			}
			if err := j.observe(ctx, env); err != nil {
				return err
			}
		default:
			return nil
		}
	}
	return nil
}
