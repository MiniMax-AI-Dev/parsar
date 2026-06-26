package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func stubFactory(marker string) agent.Factory {
	return func(_ context.Context, _ proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
		return stubSession{marker: marker}, nil
	}
}

type stubSession struct{ marker string }

func (stubSession) Cancel(context.Context) error { return nil }
func (stubSession) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return nil
}
func (stubSession) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return nil
}

func TestRegistryResolveReturnsRegisteredFactory(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register("claude_code", stubFactory("cc"))

	f, err := reg.Resolve("claude_code")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sess, err := f(context.Background(), proto.PromptRequestPayload{AgentKind: "claude_code"}, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	stub, ok := sess.(stubSession)
	if !ok || stub.marker != "cc" {
		t.Errorf("resolved factory returned %#v, want stubSession{marker:\"cc\"}", sess)
	}
}

func TestRegistryResolveUnknownKindReturnsTypedError(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register("claude_code", stubFactory("cc"))

	_, err := reg.Resolve("opencode")
	if !errors.Is(err, agent.ErrUnsupportedKind) {
		t.Errorf("Resolve unknown = %v, want ErrUnsupportedKind chain", err)
	}
}

func TestRegistryRegisterOverwrites(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register("k", stubFactory("v1"))
	reg.Register("k", stubFactory("v2"))

	f, err := reg.Resolve("k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sess, _ := f(context.Background(), proto.PromptRequestPayload{}, nil)
	if got := sess.(stubSession).marker; got != "v2" {
		t.Errorf("overwrite: marker = %q, want v2", got)
	}
}

func TestRegistryKindsReportsRegistered(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register("claude_code", stubFactory("cc"))
	reg.Register("opencode", stubFactory("oc"))

	got := reg.Kinds()
	slices.Sort(got)
	want := []string{"claude_code", "opencode"}
	if !slices.Equal(got, want) {
		t.Errorf("Kinds = %v, want %v", got, want)
	}
}

func TestRegistryRegisterPanicsOnEmptyKind(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(\"\", ...) did not panic")
		}
	}()
	agent.NewRegistry().Register("", stubFactory("x"))
}

func TestRegistryRegisterPanicsOnNilFactory(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(kind, nil) did not panic")
		}
	}()
	agent.NewRegistry().Register("k", nil)
}

func TestRegistrySupportedAgentKindsReportsDescriptors(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "opencode",
		Available: false,
		Version:   "missing",
		Capabilities: proto.AgentKindCapabilities{
			Streaming: true,
		},
	}, stubFactory("oc"))
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "claude_code",
		Available: true,
		Version:   "1.2.3",
		Capabilities: proto.AgentKindCapabilities{
			Streaming:   true,
			Permissions: true,
			Usage:       true,
			Resume:      true,
		},
	}, stubFactory("cc"))

	got := reg.SupportedAgentKinds()
	if len(got) != 2 {
		t.Fatalf("SupportedAgentKinds len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Kind != "claude_code" || got[1].Kind != "opencode" {
		t.Fatalf("SupportedAgentKinds sort = %#v, want claude_code then opencode", got)
	}
	if !got[0].Available || got[0].Version != "1.2.3" || !got[0].Capabilities.Permissions || !got[0].Capabilities.Resume {
		t.Fatalf("claude_code descriptor not preserved: %#v", got[0])
	}
	if got[1].Available || got[1].Version != "missing" || !got[1].Capabilities.Streaming {
		t.Fatalf("opencode descriptor not preserved: %#v", got[1])
	}
}
