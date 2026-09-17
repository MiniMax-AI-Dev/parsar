package gateway

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPreparationSubscriptionHasNoRunIdentityAndOrdersRevisions(t *testing.T) {
	registry := NewRegistry()
	s := NewSession(newFakeConn(), "device", "tenant", "test", registry, nil)
	defer s.Close("test")
	sub, err := s.SubscribePreparation("request")
	if err != nil {
		t.Fatal(err)
	}
	defer s.UnsubscribePreparation("request")
	if registry.LookupRun("request") != nil {
		t.Fatal("preparation registered a fake run")
	}
	for _, status := range []proto.PreparationStatusPayload{
		{Handle: "handle", State: "ready", Revision: 2},
		{Handle: "handle", State: "preparing", Revision: 1},
		{Handle: "foreign-handle", State: "failed", Revision: 100},
		{Handle: "handle", State: "released", Revision: 3},
		{Handle: "handle", State: "ready", Revision: 2},
	} {
		env, err := proto.NewEnvelope(proto.TypePreparationStatus, "request", status)
		if err != nil {
			t.Fatal(err)
		}
		s.dispatch(env)
	}
	var states []string
	for env := range sub.Events {
		var status proto.PreparationStatusPayload
		if err := env.DecodePayload(&status); err != nil {
			t.Fatal(err)
		}
		states = append(states, status.State)
	}
	if len(states) != 2 || states[0] != "ready" || states[1] != "released" || sub.Err() != nil {
		t.Fatal(states, sub.Err())
	}
}

func TestPreparationCloseAndOverflowDoNotInventRunEvents(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", NewRegistry(), nil)
		sub, err := s.SubscribePreparation("request")
		if err != nil {
			t.Fatal(err)
		}
		if overflow {
			for revision := uint64(1); revision <= 17; revision++ {
				env, _ := proto.NewEnvelope(proto.TypePreparationStatus, "request", proto.PreparationStatusPayload{Handle: "handle", State: "ready", Revision: revision})
				s.dispatch(env)
			}
		} else {
			s.Close("disconnect")
		}
		for env := range sub.Events {
			if env.Type != proto.TypePreparationStatus {
				t.Fatal("invented run event", env.Type)
			}
		}
		want := ErrSessionClosed
		if overflow {
			want = ErrSubscriberOverflow
		}
		if !errors.Is(sub.Err(), want) {
			t.Fatal(sub.Err())
		}
		s.Close("test")
	}
}

func TestPreparationCapabilitySurvivesHeartbeatMapping(t *testing.T) {
	kinds := deviceKindsFromHeartbeat(proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Preparation: true, RemoteEnvironment: true, WorkspaceReadPreparation: true}}}})
	if len(kinds) != 1 || (!kinds[0].Capabilities.Preparation || !kinds[0].Capabilities.WorkspaceReadPreparation) {
		t.Fatal("preparation capability lost")
	}
}
