package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type pendingFunction struct {
	call    proto.FunctionCallPayload
	result  *proto.FunctionResultPayload
	receipt chan error
	applied bool
}

type functionState struct {
	mu     sync.Mutex
	calls  map[string]*pendingFunction
	closed bool
}

func validateFunctions(tools []proto.FunctionTool) error {
	names := map[string]bool{}
	for _, tool := range tools {
		var schema struct {
			Type string `json:"type"`
		}
		if strings.TrimSpace(tool.Name) == "" || names[tool.Name] || json.Unmarshal(tool.Parameters, &schema) != nil || schema.Type != "object" {
			return fmt.Errorf("claudesdk: functions require unique names and object-root JSON schemas")
		}
		names[tool.Name] = true
	}
	return nil
}

func (s *session) receiveFunction(event bridgeEvent, start startRequest, emit func(string, any)) error {
	var call *proto.FunctionCallPayload
	var observation *proto.ToolObservation
	var receipt chan error
	err := func() error {
		s.functions.mu.Lock()
		defer s.functions.mu.Unlock()
		if s.functions.closed {
			return agent.ErrUnknownFunctionCall
		}
		switch event.Type {
		case "function_call":
			call = event.Call
			declared := false
			if call != nil {
				for _, tool := range start.Functions {
					if tool.Name == call.Name {
						declared = true
						break
					}
				}
			}
			if !declared || call.CallID == "" || !json.Valid(call.Arguments) || s.functions.calls[call.CallID] != nil {
				return fmt.Errorf("claudesdk: invalid function call")
			}
			s.functions.calls[call.CallID] = &pendingFunction{call: *call, receipt: make(chan error, 1)}
			observation = &proto.ToolObservation{Kind: "function", Status: "in_progress", Name: call.Name, Arguments: call.Arguments}
		case "function_applied":
			pending := s.functions.calls[event.CallID]
			if pending == nil || pending.result == nil || pending.applied || pending.result.DeliveryID != event.DeliveryID {
				return fmt.Errorf("claudesdk: invalid native function receipt")
			}
			pending.applied = true
			receipt = pending.receipt
			status := "completed"
			if !pending.result.Success {
				status = "failed"
			}
			observation = &proto.ToolObservation{Kind: "function", Status: status, Name: pending.call.Name, Arguments: pending.call.Arguments, Content: &pending.result.Content}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if start.observeFunctions {
		id, stage := event.CallID, "after"
		if call != nil {
			id, stage = call.CallID, "before"
		}
		emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: observation.Name, Stage: stage, Observation: observation})
	}
	if call != nil {
		emit(proto.TypeFunctionCall, call)
	} else {
		receipt <- nil
	}
	return nil
}

func (s *session) SubmitFunctionResult(ctx context.Context, result proto.FunctionResultPayload) error {
	if result.CallID == "" || result.DeliveryID == "" {
		return fmt.Errorf("claudesdk: function result identities are required")
	}
	if err := result.ValidateContent(); err != nil {
		return err
	}
	for _, part := range result.Content {
		if part.Type != "input_text" {
			return fmt.Errorf("claudesdk: image function results are not supported")
		}
	}
	data, err := json.Marshal(struct {
		Type string `json:"type"`
		proto.FunctionResultPayload
	}{Type: "function_result", FunctionResultPayload: result})
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return fmt.Errorf("claudesdk: function result exceeds bridge input limit")
	}
	var owned proto.FunctionResultPayload
	if err := json.Unmarshal(data, &owned); err != nil {
		return err
	}
	s.functions.mu.Lock()
	pending := s.functions.calls[result.CallID]
	if s.functions.closed || s.process.Context().Err() != nil || pending == nil || pending.result != nil {
		s.functions.mu.Unlock()
		return agent.ErrUnknownFunctionCall
	}
	pending.result = &owned
	s.functions.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A lost receipt has an unknown outcome; terminate this execution instead of resending.
	stop := context.AfterFunc(ctx, s.process.Cancel)
	defer stop()
	s.writeMu.Lock()
	if err = ctx.Err(); err == nil {
		_, err = s.process.Stdin.Write(append(data, '\n'))
	}
	s.writeMu.Unlock()
	if err != nil {
		s.process.Cancel()
		return fmt.Errorf("claudesdk: function result transport failed")
	}
	select {
	case err := <-pending.receipt:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) functionsComplete() bool {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	for _, pending := range s.functions.calls {
		if !pending.applied {
			return false
		}
	}
	return true
}

func (s *session) stopFunctions() {
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	if s.functions.closed {
		return
	}
	s.functions.closed = true
	for _, pending := range s.functions.calls {
		if !pending.applied {
			pending.receipt <- agent.ErrUnknownFunctionCall
		}
	}
}
