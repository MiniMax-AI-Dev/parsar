package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type session struct {
	process   *clirunner.Process
	writeMu   sync.Mutex
	functions functionState
	steering  steeringState
	settled   chan struct{}
	outcome   proto.DonePayload
}

func NewFactory(config Config) agent.Factory {
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if out == nil {
			return nil, fmt.Errorf("claudesdk: output channel is required")
		}
		if config.Workspace != nil {
			runID, prompt := req.RunID, req.Prompt
			if strings.TrimSpace(runID) == "" || strings.TrimSpace(prompt) == "" {
				return nil, fmt.Errorf("claudesdk: run id and prompt are required")
			}
			req.RunID, req.Prompt = "", ""
			prepared, err := NewPreparationFactory(config)(ctx, req)
			if err != nil {
				return nil, err
			}
			defer prepared.Close()
			return prepared.Start(ctx, runID, prompt, out)
		}
		start, env, err := prepare(config, req)
		if err != nil {
			return nil, err
		}
		if start.MCPHTTPServers != nil {
			info, err := CheckRuntime(ctx, config)
			if err != nil || !info.SupportsHTTPMCP() {
				return nil, fmt.Errorf("claudesdk: packaged runtime does not support HTTP MCP")
			}
			for _, server := range *start.MCPHTTPServers {
				if server.BearerTokenEnvVar != "" && !info.SupportsHTTPMCPBearer() {
					return nil, fmt.Errorf("claudesdk: packaged runtime does not support authenticated HTTP MCP")
				}
			}
		}
		s, err := launch(ctx, config, start, env)
		if err != nil {
			return nil, err
		}
		go s.run(ctx, req.RunID, start, out, nil)
		return s, nil
	}
}

type bridgeEvent struct {
	InputID     string                      `json:"input_id"`
	ResultID    string                      `json:"result_id"`
	Usage       json.RawMessage             `json:"usage,omitempty"`
	Type        string                      `json:"type"`
	Delta       string                      `json:"delta"`
	SessionID   string                      `json:"session_id"`
	Text        string                      `json:"text"`
	Code        string                      `json:"code"`
	ItemID      string                      `json:"item_id"`
	Message     *proto.OutputMessagePayload `json:"message"`
	Call        *proto.FunctionCallPayload  `json:"call"`
	CallID      string                      `json:"call_id"`
	DeliveryID  string                      `json:"delivery_id"`
	ID          string                      `json:"id"`
	Stage       string                      `json:"stage"`
	Observation *proto.ToolObservation      `json:"observation"`
}

func (s *session) run(ctx context.Context, runID string, start startRequest, out chan<- proto.Envelope, prepared *prepared) {
	defer func() {
		if out != nil {
			close(out)
		}
	}()
	defer s.stopFunctions()
	defer s.stopSteering()
	emit := func(kind string, payload any) {
		event, err := proto.NewEnvelope(kind, runID, payload)
		if err != nil {
			return
		}
		// Cancellation must drain native output even if the event consumer stops.
		// Terminal publication follows settlement and cannot hold up Cancel.
		var stopping <-chan struct{}
		if kind != proto.TypeError && kind != proto.TypeDone {
			stopping = s.process.Context().Done()
		}
		// Preserve drained observations when the consumer can accept them immediately.
		select {
		case out <- event:
			return
		default:
		}
		select {
		case out <- event:
		case <-ctx.Done():
		case <-stopping:
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
	if prepared != nil {
		binding, err := prepared.awaitStart(scanner, failure)
		if binding == nil {
			prepared.failure = s.drain(scanner, stderrDone, err)
			close(s.settled)
			return
		}
		runID, out = binding.runID, binding.out
		if err := json.NewEncoder(s.process.Stdin).Encode(struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
		}{Type: "start", Prompt: binding.prompt}); err != nil {
			failure = fmt.Errorf("claudesdk: cannot submit SDK input")
			s.process.Cancel()
		}
	}
	var content strings.Builder
	var result *bridgeEvent
	var usage proto.Usage
	var usageSession string
	usageIDs := map[string]bool{}
	var sequence uint64
	terminal := false
	mcp := mcpState{calls: map[string]proto.ToolObservation{}}
	commands := commandState{calls: map[string]proto.ToolObservation{}}
	for scanner.Scan() {
		var event bridgeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || terminal {
			failure = fmt.Errorf("claudesdk: invalid SDK bridge output")
			s.process.Cancel()
			break
		}
		switch event.Type {
		case "command_observation":
			if err := commands.receive(event, start, s.inputSessionID(), emit); err != nil {
				failure = err
				s.process.Cancel()
			}
		case "mcp_observation":
			if err := mcp.receive(event, start, emit); err != nil {
				failure = err
				s.process.Cancel()
			}
		case "delta":
			if start.ObserveMessages && event.ItemID == "" || !start.ObserveMessages && event.ItemID != "" {
				failure = fmt.Errorf("claudesdk: invalid message delta identity")
				s.process.Cancel()
				break
			}
			content.WriteString(event.Delta)
			sequence++
			emit(proto.TypeDelta, proto.DeltaPayload{ItemID: event.ItemID, Delta: event.Delta, Sequence: sequence})
		case "output_message":
			message := event.Message
			if !start.ObserveMessages || message == nil || message.ID == "" ||
				(message.Status != "in_progress" && message.Status != "completed") ||
				(message.Status == "completed") != (message.Text != nil) {
				failure = fmt.Errorf("claudesdk: invalid message observation")
				s.process.Cancel()
				break
			}
			emit(proto.TypeOutputMessage, message)
		case "function_call", "function_applied":
			if err := s.receiveFunction(event, start, emit); err != nil {
				failure = err
				s.process.Cancel()
			}
		case "input_ready", "input_closed", "input_applied", "input_rejected":
			if err := s.receiveInput(event, start); err != nil {
				failure = err
				s.process.Cancel()
			}
		case "usage":
			if event.ResultID == "" || !s.matchesInputSession(event.SessionID) || event.SessionID == "" || (start.Resume != "" && event.SessionID != start.Resume) ||
				(usageSession != "" && usageSession != event.SessionID) || usageIDs[event.ResultID] {
				failure = fmt.Errorf("claudesdk: invalid usage identity or duplicate result")
				s.process.Cancel()
				break
			}
			nextUsage, err := appendNativeUsage(usage, event.Usage)
			failure = err
			if failure != nil {
				s.process.Cancel()
				break
			}
			usage = nextUsage
			usageIDs[event.ResultID] = true
			usageSession = event.SessionID
			emit(proto.TypeUsage, proto.UsagePayload{Usage: usage})
		case "result":
			if !s.matchesInputSession(event.SessionID) || event.SessionID == "" || start.Resume != "" && event.SessionID != start.Resume || usageSession != "" && event.SessionID != usageSession || !s.functionsComplete() || !s.steeringComplete() || !mcp.complete() || !commands.complete() {
				failure = fmt.Errorf("claudesdk: invalid native completion or unconfirmed input/result")
				s.process.Cancel()
			} else {
				result = &event
			}
			terminal = true
		case "error":
			failure = bridgeFailure(event.Code)
			terminal = true
		default:
			failure = fmt.Errorf("claudesdk: unknown SDK bridge event")
			s.process.Cancel()
		}
		if failure != nil && !terminal {
			break
		}
	}
	failure = s.drain(scanner, stderrDone, failure)
	if result == nil && failure == nil {
		failure = fmt.Errorf("claudesdk: SDK result is missing")
	}
	mcp.close(start, emit)
	commands.close(start, emit)
	s.stopFunctions()
	s.stopSteering()
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "claude_session"}
	if id := s.inputSessionID(); id != "" {
		metadata[proto.DoneMetaAgentSessionID] = id
	}
	if failure == nil {
		content.Reset()
		content.WriteString(result.Text)
		metadata[proto.DoneMetaAgentSessionID] = result.SessionID
	}
	s.outcome = proto.DonePayload{Content: content.String(), Usage: usage, Metadata: metadata}
	// Router completion cleanup calls Cancel while consuming Done. Settle first.
	close(s.settled)
	if failure != nil {
		emit(proto.TypeError, proto.ErrorPayload{Error: failure.Error()})
	}
	emit(proto.TypeDone, s.outcome)
}

func launch(ctx context.Context, config Config, start startRequest, env []string) (*session, error) {
	binary := config.Node
	if binary == "" {
		binary = "node"
	}
	process, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: []string{config.Entrypoint}, Dir: start.Cwd, Env: env, NeedStdin: true, OwnProcessGroup: true})
	if err != nil {
		return nil, err
	}
	return &session{process: process, functions: functionState{calls: map[string]*pendingFunction{}}, settled: make(chan struct{})}, nil
}

func (s *session) drain(scanner *bufio.Scanner, stderrDone <-chan struct{}, failure error) error {
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
	return failure
}

func bridgeFailure(code string) error {
	switch code {
	case "invalid_request", "history_unavailable", "execution_failed", "cancelled":
		return fmt.Errorf("claudesdk: %s", code)
	default:
		return fmt.Errorf("claudesdk: unknown SDK bridge failure")
	}
}

func (s *session) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return agent.ErrUnknownPermission
}
func (s *session) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return agent.ErrUnknownAsk
}
