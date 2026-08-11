package runstream

import (
	"context"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

const DefaultBufferSize = 64

type Broker struct {
	mu         sync.Mutex // serializes subscriber sends and closes as well as run state
	bufferSize int
	runs       map[string]*runState
}

type runState struct {
	events      []connector.PromptEvent
	subscribers map[chan connector.PromptEvent]struct{}
	closed      bool
}

func NewBroker(bufferSize int) *Broker {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}
	return &Broker{bufferSize: bufferSize, runs: map[string]*runState{}}
}

func (b *Broker) Publish(runID string, ev connector.PromptEvent) {
	if runID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateLocked(runID)
	if st.closed {
		return
	}
	st.events = append(st.events, ev)
	if len(st.events) > b.bufferSize {
		st.events = append([]connector.PromptEvent(nil), st.events[len(st.events)-b.bufferSize:]...)
	}
	for ch := range st.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *Broker) Subscribe(ctx context.Context, runID string) <-chan connector.PromptEvent {
	out := make(chan connector.PromptEvent, b.bufferSize)
	if runID == "" {
		close(out)
		return out
	}
	b.mu.Lock()
	st := b.stateLocked(runID)
	for _, ev := range st.events {
		out <- ev
	}
	if st.closed {
		close(out)
		b.mu.Unlock()
		return out
	}
	st.subscribers[out] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.unsubscribe(runID, out)
	}()
	return out
}

func (b *Broker) Finish(runID string) {
	if runID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.runs[runID]
	if !ok || st.closed {
		return
	}
	st.closed = true
	for ch := range st.subscribers {
		delete(st.subscribers, ch)
		close(ch)
	}
}

func (b *Broker) SubscriberCount(runID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.runs[runID]
	if !ok {
		return 0
	}
	return len(st.subscribers)
}

func (b *Broker) stateLocked(runID string) *runState {
	if b.runs == nil {
		b.runs = map[string]*runState{}
	}
	st := b.runs[runID]
	if st == nil {
		st = &runState{subscribers: map[chan connector.PromptEvent]struct{}{}}
		b.runs[runID] = st
	}
	return st
}

func (b *Broker) unsubscribe(runID string, ch chan connector.PromptEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.runs[runID]
	if !ok {
		return
	}
	if _, ok := st.subscribers[ch]; ok {
		delete(st.subscribers, ch)
		close(ch)
	}
}
