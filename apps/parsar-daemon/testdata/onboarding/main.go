// This synthetic harness exercises the production router without a native model.
// It is test-only, has no workspace/tools, and is never in the built-in catalog.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type sender struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (s *sender) Send(_ context.Context, e proto.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encoder.Encode(e)
}

type harness struct{ history map[string]string }

func (h *harness) start(_ context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	if !req.StrictResume || !req.ReleaseOnCompletion || !req.DisableExecutionEnvironment || !req.DisableSubagents || len(req.FunctionTools) > 0 || req.MCPHTTPServers != nil {
		return nil, errors.New("unsupported fixture operation")
	}
	previous := h.history[req.AgentStateKey]
	if req.AgentSessionID != previous || req.RequireExistingNativeSession {
		return nil, errors.New("native history mismatch")
	}
	id := previous
	if id == "" {
		id = "fixture-" + req.AgentStateKey
		h.history[req.AgentStateKey] = id
	}
	s := &session{out: out, run: req.RunID, native: id}
	s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: "ready", Sequence: 1})
	return s, nil
}

type session struct {
	mu          sync.Mutex
	out         chan<- proto.Envelope
	run, native string
	closed      bool
}

func (s *session) emit(kind string, payload any) {
	e, _ := proto.NewEnvelope(kind, s.run, payload)
	s.out <- e
}
func (s *session) Cancel(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.out)
	}
	return nil
}
func (s *session) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: "cancelled", Metadata: map[string]any{proto.DoneMetaAgentSessionID: s.native}}
}
func (s *session) Steer(ctx context.Context, p proto.PromptSteerPayload) error {
	return s.SteerWithReceipt(ctx, p, func() {})
}
func (s *session) SteerWithReceipt(_ context.Context, p proto.PromptSteerPayload, written func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return agent.ErrSteeringInactive
	}
	written()
	s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: p.Text, Sequence: 2})
	s.emit(proto.TypeDone, proto.DonePayload{Content: "ready" + p.Text, Metadata: map[string]any{proto.DoneMetaAgentSessionID: s.native}})
	s.closed = true
	close(s.out)
	return nil
}

func run() error {
	registry := agent.NewRegistry()
	h := &harness{history: map[string]string{}}
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture_harness", Available: true, Capabilities: proto.AgentKindCapabilities{
		Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, ExecutionControls: true, ToolObservations: true, SubagentControl: true, EnvironmentNone: true,
	}}, h.start)
	sink := &sender{encoder: json.NewEncoder(os.Stdout)}
	router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sink, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return err
	}
	defer router.Shutdown(context.Background())
	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: registry.SupportedAgentKinds()})
	if err = sink.Send(context.Background(), heartbeat); err != nil {
		return err
	}
	decoder := json.NewDecoder(os.Stdin)
	for {
		var e proto.Envelope
		if err = decoder.Decode(&e); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if err = router.Handle(context.Background(), e); err != nil {
			return err
		}
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
