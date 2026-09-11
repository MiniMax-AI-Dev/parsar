package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type recoveringWriter struct {
	fail   bool
	events []store.ExecutionEvent
}

type ambiguousWriter struct {
	firstCount int
	written    int
}

func (w *ambiguousWriter) AppendTurnEvents(_ context.Context, _, _, _ string, first int32, events []store.ExecutionEvent) error {
	if w.firstCount == 0 {
		w.firstCount, w.written = len(events), len(events)
		return context.DeadlineExceeded
	}
	if first == 1 {
		if len(events) != w.firstCount {
			return store.ErrIdempotencyConflict
		}
		return nil
	}
	if int(first) != w.written+1 {
		return errors.New("wrong next batch ordinal")
	}
	w.written += len(events)
	return nil
}

func TestJournalKeepsBatchIdentityAfterAnUncertainCommit(t *testing.T) {
	writer := &ambiguousWriter{}
	j := journal{store: writer, next: 1}
	for i := range 3 {
		env, _ := proto.NewEnvelope(proto.TypeDelta, "run", proto.DeltaPayload{Delta: "text", Sequence: uint64(i + 1)})
		if err := j.observe(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.flush(context.Background()); err == nil {
		t.Fatal("expected uncertain commit")
	}
	env, _ := proto.NewEnvelope(proto.TypeDelta, "run", proto.DeltaPayload{Delta: "later", Sequence: 4})
	if err := j.observe(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if err := j.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writer.written != 4 || j.next != 5 {
		t.Fatalf("lost or duplicated events: written=%d next=%d", writer.written, j.next)
	}
}

func (w *recoveringWriter) AppendTurnEvents(_ context.Context, _, _, _ string, first int32, events []store.ExecutionEvent) error {
	if w.fail {
		w.fail = false
		return context.DeadlineExceeded
	}
	if int(first) != len(w.events)+1 || len(events) > 64 {
		return errors.New("unexpected batch boundary")
	}
	w.events = append(w.events, events...)
	return nil
}

func TestJournalRetainsTriggeringFrameAcrossFlushFailure(t *testing.T) {
	for _, size := range []int{10, 300 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			writer := &recoveringWriter{fail: true}
			j := journal{store: writer, next: 1}
			var expected []string
			for i := range 65 {
				env, _ := proto.NewEnvelope(proto.TypeDelta, "run", proto.DeltaPayload{Delta: strings.Repeat("x", size), Sequence: uint64(i + 1)})
				expected = append(expected, string(env.Payload))
				if err := j.observe(context.Background(), env); err != nil {
					break
				}
			}
			if writer.fail {
				t.Fatal("flush failure was not exercised")
			}
			if err := j.flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(writer.events) != len(expected) {
				t.Fatalf("lost received frame: got %d want %d", len(writer.events), len(expected))
			}
			for i, event := range writer.events {
				if string(event.Payload) != expected[i] {
					t.Fatalf("frame %d changed", i)
				}
			}
		})
	}
}

func TestJournalDrainRetainsTerminalContinuityAndUsageOnFailure(t *testing.T) {
	writer := &recoveringWriter{fail: true}
	j := journal{store: writer, next: 1}
	upstream := make(chan proto.Envelope, 64)
	for i := range 63 {
		env, _ := proto.NewEnvelope(proto.TypeDelta, "run", proto.DeltaPayload{Delta: "partial", Sequence: uint64(i + 1)})
		upstream <- env
	}
	done, _ := proto.NewEnvelope(proto.TypeDone, "run", proto.DonePayload{Content: "Final", Usage: proto.Usage{InputTokens: 10, OutputTokens: 4}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-thread"}})
	upstream <- done
	close(upstream)
	result := Result{ErrorCode: "event_persistence_failed"}
	if err := j.drain(upstream, &result); err != nil {
		t.Fatal(err)
	}
	if err := j.flush(context.Background()); err == nil {
		t.Fatal("flush should fail")
	}
	if result.ErrorCode != "event_persistence_failed" || result.Done.Metadata[proto.DoneMetaAgentSessionID] != "native-thread" || result.Done.Usage.InputTokens != 10 || result.Done.Usage.OutputTokens != 4 {
		t.Fatalf("terminal data lost or failure cleared: %+v", result)
	}
	if err := j.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(writer.events) != 64 || writer.events[63].Kind != proto.TypeDone {
		t.Fatal("terminal frame lost")
	}
}

func TestCancellationReceiptContinuitySurvivesFlushFailure(t *testing.T) {
	writer := &recoveringWriter{fail: true}
	j := &journal{store: writer, next: 1}
	for i := range 63 {
		env, _ := proto.NewEnvelope(proto.TypeDelta, "run", proto.DeltaPayload{Delta: "partial", Sequence: uint64(i + 1)})
		if err := j.enqueue(env); err != nil {
			t.Fatal(err)
		}
	}
	result := Result{ErrorCode: "event_persistence_failed"}
	reply := cancellationResult{ack: proto.InteractionDecisionAckPayload{Applied: true, Outcome: &proto.DonePayload{Usage: proto.Usage{InputTokens: 9}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancel-native"}}}}
	if err := recordCancellation(context.Background(), j, reply, &result); err == nil {
		t.Fatal("flush should fail")
	}
	if result.Done.Metadata[proto.DoneMetaAgentSessionID] != "cancel-native" || result.Done.Usage.InputTokens != 9 || result.ErrorCode != "event_persistence_failed" {
		t.Fatalf("lost cancellation outcome: %+v", result)
	}
	if err := j.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(writer.events) != 64 {
		t.Fatal("receipt or partial text lost")
	}
}
