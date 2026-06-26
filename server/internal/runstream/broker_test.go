package runstream

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

func TestSubscribeLateStillGetsFinal(t *testing.T) {
	b := NewBroker(64)
	runID := "run-late"
	b.Publish(runID, connector.PromptEvent{Type: connector.EventDelta, Sequence: 1, Delta: "hello"})
	b.Publish(runID, connector.PromptEvent{Type: connector.EventDone, Sequence: 2, Final: &connector.PromptOutput{Content: "hello"}})
	b.Finish(runID)

	events := drain(t, b.Subscribe(context.Background(), runID), 2)
	if len(events) != 2 || events[1].Type != connector.EventDone || events[1].Final.Content != "hello" {
		t.Fatalf("late events = %+v, want replay ending with done final", events)
	}
}

func TestCancelCleansUp(t *testing.T) {
	b := NewBroker(64)
	ctx, cancel := context.WithCancel(context.Background())
	ch := b.Subscribe(ctx, "run-cancel")
	if got := b.SubscriberCount("run-cancel"); got != 1 {
		t.Fatalf("subscribers = %d, want 1", got)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscriber channel should close after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber cleanup")
	}
	if got := b.SubscriberCount("run-cancel"); got != 0 {
		t.Fatalf("subscribers after cancel = %d, want 0", got)
	}
}

func TestMultiSubscriber(t *testing.T) {
	b := NewBroker(64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch1 := b.Subscribe(ctx, "run-multi")
	ch2 := b.Subscribe(ctx, "run-multi")
	b.Publish("run-multi", connector.PromptEvent{Type: connector.EventDelta, Sequence: 1, Delta: "x"})
	if ev := mustRead(t, ch1); ev.Delta != "x" {
		t.Fatalf("ch1 event = %+v", ev)
	}
	if ev := mustRead(t, ch2); ev.Delta != "x" {
		t.Fatalf("ch2 event = %+v", ev)
	}
}

func TestRingBufferReplay(t *testing.T) {
	b := NewBroker(3)
	for i := uint64(1); i <= 5; i++ {
		b.Publish("run-ring", connector.PromptEvent{Type: connector.EventDelta, Sequence: i})
	}
	b.Finish("run-ring")
	events := drain(t, b.Subscribe(context.Background(), "run-ring"), 3)
	if got := []uint64{events[0].Sequence, events[1].Sequence, events[2].Sequence}; got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("replayed seq = %v, want [3 4 5]", got)
	}
}

// TestPublishAfterFinishIsDropped pins the contract that once a run is
// Finish()'d, late Publish calls MUST NOT mutate the replay buffer or
// surface to late subscribers.
func TestPublishAfterFinishIsDropped(t *testing.T) {
	b := NewBroker(64)
	runID := "run-after-finish"
	b.Publish(runID, connector.PromptEvent{Type: connector.EventDelta, Sequence: 1, Delta: "first"})
	b.Publish(runID, connector.PromptEvent{Type: connector.EventDone, Sequence: 2, Final: &connector.PromptOutput{Content: "first"}})
	b.Finish(runID)

	// Simulate a late publisher that didn't see Finish.
	b.Publish(runID, connector.PromptEvent{Type: connector.EventDelta, Sequence: 99, Delta: "leaked"})
	b.Publish(runID, connector.PromptEvent{Type: connector.EventError, Error: "leaked-error"})

	events := drain(t, b.Subscribe(context.Background(), runID), 2)
	if len(events) != 2 {
		t.Fatalf("late replay = %d events, want 2 (post-finish publishes must be dropped)", len(events))
	}
	if events[0].Type != connector.EventDelta || events[0].Delta != "first" {
		t.Fatalf("event[0] = %+v, want first delta", events[0])
	}
	if events[1].Type != connector.EventDone || events[1].Final == nil || events[1].Final.Content != "first" {
		t.Fatalf("event[1] = %+v, want clean done", events[1])
	}
}

func mustRead(t *testing.T, ch <-chan connector.PromptEvent) connector.PromptEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return connector.PromptEvent{}
	}
}

func drain(t *testing.T, ch <-chan connector.PromptEvent, want int) []connector.PromptEvent {
	t.Helper()
	var events []connector.PromptEvent
	deadline := time.After(time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if len(events) != want {
					t.Fatalf("got %d events, want %d", len(events), want)
				}
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out after %d/%d events", len(events), want)
		}
	}
}
