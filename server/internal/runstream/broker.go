package runstream

import (
	"context"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

const DefaultBufferSize = 64

type Broker struct {
	mu         sync.Mutex
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
	st := b.stateLocked(runID)
	if st.closed {
		b.mu.Unlock()
		return
	}
	st.events = append(st.events, ev)
	if len(st.events) > b.bufferSize {
		st.events = append([]connector.PromptEvent(nil), st.events[len(st.events)-b.bufferSize:]...)
	}
	subs := make([]chan connector.PromptEvent, 0, len(st.subscribers))
	for ch := range st.subscribers {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
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
	replay := append([]connector.PromptEvent(nil), st.events...)
	closed := st.closed
	if !closed {
		st.subscribers[out] = struct{}{}
	}
	b.mu.Unlock()
	go func() {
		defer func() {
			if !closed {
				b.unsubscribe(runID, out)
			}
		}()
		for _, ev := range replay {
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
		if closed {
			close(out)
			return
		}
		<-ctx.Done()
	}()
	return out
}

func (b *Broker) Finish(runID string) {
	if runID == "" {
		return
	}
	b.mu.Lock()
	st, ok := b.runs[runID]
	if !ok || st.closed {
		b.mu.Unlock()
		return
	}
	st.closed = true
	subs := make([]chan connector.PromptEvent, 0, len(st.subscribers))
	for ch := range st.subscribers {
		subs = append(subs, ch)
		delete(st.subscribers, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
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
