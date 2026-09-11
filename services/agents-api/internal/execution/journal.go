package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type journal struct {
	store                 eventWriter
	tenant, session, turn string
	next                  int32
	batch                 []store.ExecutionEvent
	bytes                 int
	pendingCount          int
}

type eventWriter interface {
	AppendTurnEvents(context.Context, string, string, string, int32, []store.ExecutionEvent) error
}

func recordCancellation(ctx context.Context, journal *journal, reply cancellationResult, result *Result) error {
	if reply.err != nil {
		return nil
	}
	env, err := proto.NewEnvelope("cancel_receipt", journal.turn, reply.ack)
	if err != nil {
		return err
	}
	if reply.ack.Applied && reply.ack.Outcome != nil {
		raw, err := json.Marshal(reply.ack.Outcome)
		if err != nil {
			return err
		}
		if err := result.mergeDone(raw); err != nil {
			return err
		}
	}
	return journal.observe(ctx, env)
}

func (j *journal) observe(ctx context.Context, env proto.Envelope) error {
	if err := j.enqueue(env); err != nil {
		return err
	}
	if j.bytes > 768*1024 || len(j.batch) >= 64 {
		return j.flush(ctx)
	}
	return nil
}

func (j *journal) enqueue(env proto.Envelope) error {
	switch env.Type {
	case proto.TypeDelta, proto.TypeThinking, proto.TypeToolCall, proto.TypeUsage,
		proto.TypeError, proto.TypeDone, proto.TypePromptSteerAck, "cancel_receipt":
	default:
		return nil
	}
	if len(env.Payload) > 512*1024 {
		return store.ErrEventLimit
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
	for len(j.batch) > 0 {
		count, size := 0, 0
		limit := 64
		if j.pendingCount > 0 {
			limit = j.pendingCount
		}
		for count < len(j.batch) && count < limit {
			length := len(j.batch[count].Payload)
			if count > 0 && size+length > 768*1024 {
				break
			}
			size += length
			count++
		}
		// An uncertain commit must retry the same batch even after more frames arrive.
		j.pendingCount = count
		if err := j.store.AppendTurnEvents(ctx, j.tenant, j.session, j.turn, j.next, j.batch[:count]); err != nil {
			return err
		}
		j.pendingCount = 0
		j.next += int32(count)
		j.bytes -= size
		j.batch = j.batch[count:]
	}
	j.batch = nil
	return nil
}

// Cancellation receipts use a separate waiter; preceding frames can still be queued.
func (j *journal) drain(upstream <-chan proto.Envelope, result *Result) error {
	var observedErr error
	for range 256 {
		select {
		case env, ok := <-upstream:
			if !ok {
				return observedErr
			}
			if err := j.enqueue(env); err != nil {
				observedErr = err
			}
			if err := result.mergeObservation(env); err != nil {
				observedErr = err
			}
		default:
			return observedErr
		}
	}
	return observedErr
}
