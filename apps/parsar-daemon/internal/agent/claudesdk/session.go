package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type session struct{ process *clirunner.Process }

func NewFactory(config Config) agent.Factory {
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if out == nil {
			return nil, fmt.Errorf("claudesdk: output channel is required")
		}
		start, env, err := prepare(config, req)
		if err != nil {
			return nil, err
		}
		binary := config.Node
		if binary == "" {
			binary = "node"
		}
		process, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: []string{config.Entrypoint}, Dir: start.Cwd, Env: env, NeedStdin: true, OwnProcessGroup: true})
		if err != nil {
			return nil, err
		}
		s := &session{process: process}
		go s.run(ctx, req.RunID, start, out)
		return s, nil
	}
}

type bridgeEvent struct {
	Type      string `json:"type"`
	Delta     string `json:"delta"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Code      string `json:"code"`
}

func (s *session) run(ctx context.Context, runID string, start startRequest, out chan<- proto.Envelope) {
	defer close(out)
	emit := func(kind string, payload any) {
		event, err := proto.NewEnvelope(kind, runID, payload)
		if err != nil {
			return
		}
		select {
		case out <- event:
		case <-ctx.Done():
		}
	}
	stderrDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, s.process.Stderr); close(stderrDone) }()
	var failure error
	if err := json.NewEncoder(s.process.Stdin).Encode(start); err != nil {
		failure = fmt.Errorf("claudesdk: cannot submit SDK input")
		s.process.Cancel()
	}
	scanner := bufio.NewScanner(s.process.Stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var content strings.Builder
	var result *bridgeEvent
	var sequence uint64
	terminal := false
	for scanner.Scan() {
		var event bridgeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || terminal {
			failure = fmt.Errorf("claudesdk: invalid SDK bridge output")
			s.process.Cancel()
			break
		}
		switch event.Type {
		case "delta":
			content.WriteString(event.Delta)
			sequence++
			emit(proto.TypeDelta, proto.DeltaPayload{Delta: event.Delta, Sequence: sequence})
		case "result":
			if event.SessionID == "" || start.Resume != "" && event.SessionID != start.Resume {
				failure = fmt.Errorf("claudesdk: invalid native session identity")
				s.process.Cancel()
			} else {
				result = &event
			}
			terminal = true
		case "error":
			switch event.Code {
			case "invalid_request", "history_unavailable", "execution_failed", "cancelled":
				failure = fmt.Errorf("claudesdk: %s", event.Code)
			default:
				failure = fmt.Errorf("claudesdk: unknown SDK bridge failure")
			}
			terminal = true
		default:
			failure = fmt.Errorf("claudesdk: unknown SDK bridge event")
			s.process.Cancel()
		}
		if failure != nil && !terminal {
			break
		}
	}
	if scanner.Err() != nil {
		failure = fmt.Errorf("claudesdk: SDK bridge output read failed")
		s.process.Cancel()
	}
	// Drain remaining output before Wait closes the owned readers.
	_, _ = io.Copy(io.Discard, s.process.Stdout)
	<-stderrDone
	if err := s.process.Wait(); err != nil && failure == nil {
		failure = fmt.Errorf("claudesdk: SDK process failed")
	}
	if result == nil && failure == nil {
		failure = fmt.Errorf("claudesdk: SDK result is missing")
	}
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "claude_session"}
	if failure != nil {
		emit(proto.TypeError, proto.ErrorPayload{Error: failure.Error()})
	} else {
		content.Reset()
		content.WriteString(result.Text)
		metadata[proto.DoneMetaAgentSessionID] = result.SessionID
	}
	emit(proto.TypeDone, proto.DonePayload{Content: content.String(), Metadata: metadata})
}

func (s *session) Cancel(context.Context) error { s.process.Cancel(); return nil }
func (s *session) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return agent.ErrUnknownPermission
}
func (s *session) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return agent.ErrUnknownAsk
}
