package gateway

import (
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestDurableSubscriptionOverflowIsExplicitAndIsolated(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "0.2.0", NewRegistry(), nil)
	defer s.Close("test finished")
	sub, err := s.SubscribeDurable("slow")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.SubscribeDurable("other")
	for range 257 {
		s.dispatchToSubscriber(proto.Envelope{ID: "slow", Type: proto.TypeDelta})
	}
	count := 0
	for range sub.Events {
		count++
	}
	if count != 256 || !errors.Is(sub.Err(), ErrSubscriberOverflow) {
		t.Fatalf("count=%d error=%v", count, sub.Err())
	}
	s.dispatchToSubscriber(proto.Envelope{ID: "slow", Type: proto.TypeDone})
	s.dispatchToSubscriber(proto.Envelope{ID: "other", Type: proto.TypeDone})
	if event := <-other.Events; event.Type != proto.TypeDone || other.Err() != nil {
		t.Fatal("overflow affected another run")
	}
}

func TestProductSubscriptionRetainsBestEffortBuffer(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "0.2.0", NewRegistry(), nil)
	defer s.Close("test finished")
	events, _ := s.Subscribe("product")
	for range 33 {
		s.dispatchToSubscriber(proto.Envelope{ID: "product", Type: proto.TypeDelta})
	}
	if len(events) != 32 {
		t.Fatal("product buffer changed")
	}
	for range 32 {
		<-events
	}
	s.dispatchToSubscriber(proto.Envelope{ID: "product", Type: proto.TypeDone})
	if event := <-events; event.Type != proto.TypeDone {
		t.Fatal(event.Type)
	}
	if _, ok := <-events; ok {
		t.Fatal("terminal stream not closed")
	}
}

func TestSubscriptionCloseAndDispatchAreSerialized(t *testing.T) {
	for range 100 {
		s := NewSession(newFakeConn(), "device", "tenant", "0.2.0", NewRegistry(), nil)
		sub, _ := s.SubscribeDurable("run")
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 64 {
				s.dispatchToSubscriber(proto.Envelope{ID: "run", Type: proto.TypeDelta})
			}
		}()
		go func() { defer wg.Done(); s.Unsubscribe("run") }()
		go func() { defer wg.Done(); s.Close("disconnected") }()
		wg.Wait()
		for range sub.Events {
		}
	}
}
