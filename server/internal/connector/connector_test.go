package connector_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// stubConnector lets us exercise Registry behaviour without pulling in
// any real adapter package.
type stubConnector struct {
	connectorType string
	caps          connector.Capabilities
	prompt        func(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error)
}

func (s *stubConnector) Type() string                         { return s.connectorType }
func (s *stubConnector) Capabilities() connector.Capabilities { return s.caps }

func (s *stubConnector) Prompt(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	if s.prompt == nil {
		return connector.PromptOutput{}, nil
	}
	return s.prompt(ctx, in)
}

func (s *stubConnector) StreamPrompt(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	if !s.caps.Streaming {
		return nil, connector.ErrNotSupported
	}
	ch := make(chan connector.PromptEvent, 1)
	ch <- connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{Content: "ok"}}
	close(ch)
	return ch, nil
}

func (s *stubConnector) Cancel(ctx context.Context, conversationID string) error {
	if !s.caps.Cancellation {
		return connector.ErrNotSupported
	}
	return nil
}

func (s *stubConnector) Abort(ctx context.Context, input connector.AbortInput) error {
	if !s.caps.Cancellation {
		return connector.ErrNotSupported
	}
	return nil
}

func (s *stubConnector) SubmitPermission(ctx context.Context, decision connector.PermissionDecision) error {
	if !s.caps.Permissions {
		return connector.ErrNotSupported
	}
	return nil
}

func (s *stubConnector) SubmitPromptForUserChoice(ctx context.Context, decision connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}

func (s *stubConnector) Close(ctx context.Context, conversationID string) error { return nil }

func TestCapabilitiesString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		caps connector.Capabilities
		want string
	}{
		{
			name: "empty caps render as none placeholder so log lines do not become blank",
			caps: connector.Capabilities{},
			want: "(none)",
		},
		{
			name: "phase 1 sync-only opencode shape",
			caps: connector.Capabilities{Sync: true, Cancellation: true},
			want: "sync,cancellation",
		},
		{
			name: "phase 2 streaming opencode shape",
			caps: connector.Capabilities{Sync: true, Streaming: true, Cancellation: true, Usage: true, Audit: true},
			want: "sync,streaming,cancellation,usage,audit",
		},
		{
			name: "permissions and auth render in stable order",
			caps: connector.Capabilities{Sync: true, Permissions: true, Auth: true},
			want: "sync,permissions,auth",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.caps.String(); got != tc.want {
				t.Fatalf("Capabilities.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()
	reg := connector.NewRegistry()
	c := &stubConnector{connectorType: "agent_daemon", caps: connector.Capabilities{Sync: true}}
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := reg.Get("agent_daemon")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != c {
		t.Fatalf("Get returned a different connector instance")
	}
	if types := reg.Types(); len(types) != 1 || types[0] != "agent_daemon" {
		t.Fatalf("Types = %v, want [agent_daemon]", types)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	t.Parallel()
	reg := connector.NewRegistry()
	c := &stubConnector{connectorType: "http", caps: connector.Capabilities{Sync: true}}
	if err := reg.Register(c); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := reg.Register(c)
	if !errors.Is(err, connector.ErrDuplicateConnector) {
		t.Fatalf("second Register error = %v, want ErrDuplicateConnector", err)
	}
	if !strings.Contains(err.Error(), "http") {
		t.Fatalf("duplicate error %q should mention the connector type", err.Error())
	}
}

func TestRegistryRejectsNilAndEmptyType(t *testing.T) {
	t.Parallel()
	reg := connector.NewRegistry()
	if err := reg.Register(nil); err == nil {
		t.Fatalf("Register(nil) must error")
	}
	empty := &stubConnector{connectorType: "  ", caps: connector.Capabilities{Sync: true}}
	if err := reg.Register(empty); err == nil {
		t.Fatalf("Register(empty Type) must error")
	}
}

func TestRegistryRejectsNonSyncConnector(t *testing.T) {
	t.Parallel()
	// Every connector MUST implement Prompt; we enforce that by
	// refusing to register connectors that do not advertise
	// Capabilities.Sync = true. This catches "I forgot to set the flag"
	// before any traffic ships to a broken connector.
	reg := connector.NewRegistry()
	broken := &stubConnector{connectorType: "no-sync", caps: connector.Capabilities{Streaming: true}}
	err := reg.Register(broken)
	if err == nil || !strings.Contains(err.Error(), "Sync") {
		t.Fatalf("Register without Sync should fail with a message about Sync; got %v", err)
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	t.Parallel()
	reg := connector.NewRegistry()
	_, err := reg.Get("nope")
	if !errors.Is(err, connector.ErrUnknownConnector) {
		t.Fatalf("Get(unknown) error = %v, want ErrUnknownConnector", err)
	}
}

func TestErrNotSupportedGatesOptionalMethods(t *testing.T) {
	t.Parallel()
	// A connector that only declares Sync must reject StreamPrompt /
	// Cancel / SubmitPermission with ErrNotSupported, not with a
	// silent fake success.
	sync := &stubConnector{connectorType: "sync-only", caps: connector.Capabilities{Sync: true}}
	if _, err := sync.StreamPrompt(context.Background(), connector.PromptInput{}); !errors.Is(err, connector.ErrNotSupported) {
		t.Fatalf("StreamPrompt on sync-only connector = %v, want ErrNotSupported", err)
	}
	if err := sync.Cancel(context.Background(), "conv-1"); !errors.Is(err, connector.ErrNotSupported) {
		t.Fatalf("Cancel on sync-only connector = %v, want ErrNotSupported", err)
	}
	if err := sync.SubmitPermission(context.Background(), connector.PermissionDecision{RequestID: "p1"}); !errors.Is(err, connector.ErrNotSupported) {
		t.Fatalf("SubmitPermission on sync-only connector = %v, want ErrNotSupported", err)
	}
}

func TestPromptRoundTripsInputAndOutput(t *testing.T) {
	t.Parallel()
	got := &stubConnector{
		connectorType: "echo",
		caps:          connector.Capabilities{Sync: true},
		prompt: func(_ context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
			return connector.PromptOutput{
				Content:  "echo:" + in.TriggerMessageContent,
				Metadata: map[string]any{"run_id": in.RunID},
			}, nil
		},
	}
	out, err := got.Prompt(context.Background(), connector.PromptInput{RunID: "run-1", TriggerMessageContent: "hi"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if out.Content != "echo:hi" {
		t.Fatalf("Content = %q, want %q", out.Content, "echo:hi")
	}
	if rid, _ := out.Metadata["run_id"].(string); rid != "run-1" {
		t.Fatalf("Metadata.run_id = %v, want run-1", out.Metadata["run_id"])
	}
}
