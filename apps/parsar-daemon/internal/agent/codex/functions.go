package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type dynamicFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type functionCalls struct {
	mu          sync.Mutex
	definitions []dynamicFunctionTool
	names       map[string]bool
	pending     map[string]any
	closed      bool
}

func prepareFunctionTools(tools []proto.FunctionTool) (*functionCalls, error) {
	state := &functionCalls{names: map[string]bool{}, pending: map[string]any{}}
	if len(tools) > 64 {
		return nil, errors.New("at most 64 function tools are supported")
	}
	for _, tool := range tools {
		var schema map[string]any
		if strings.TrimSpace(tool.Name) == "" || state.names[tool.Name] || json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
			return nil, errors.New("function tools require unique names and object schemas")
		}
		state.names[tool.Name] = true
		state.definitions = append(state.definitions, dynamicFunctionTool{Type: "function", Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
	}
	return state, nil
}

func (s *Session) handleFunctionCall(raw json.RawMessage, rpcID any) (any, error) {
	var call struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace *string         `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, err
	}
	if s.functions == nil || !s.functions.names[call.Tool] || call.Namespace != nil || call.CallID == "" || call.ThreadID != s.currentThreadID() || call.TurnID == "" || !json.Valid(call.Arguments) {
		return nil, errors.New("unexpected function call")
	}
	s.functions.mu.Lock()
	if s.functions.closed || s.cancelled.Load() || s.terminal.Load() || s.functions.pending[call.CallID] != nil || len(s.functions.pending) >= 64 {
		s.functions.mu.Unlock()
		return nil, errors.New("function call cannot be admitted")
	}
	s.functions.pending[call.CallID] = rpcID
	s.functions.mu.Unlock()
	env, err := proto.NewEnvelope(proto.TypeFunctionCall, s.runID, proto.FunctionCallPayload{CallID: call.CallID, Name: call.Tool, Arguments: call.Arguments})
	if err == nil {
		err = s.sendFunctionCall(env)
	}
	if err != nil {
		s.functions.mu.Lock()
		delete(s.functions.pending, call.CallID)
		s.functions.mu.Unlock()
		return nil, err
	}
	return DeferReply, nil
}

func (s *Session) sendFunctionCall(env proto.Envelope) error {
	s.outMu.RLock()
	defer s.outMu.RUnlock()
	if s.outClosed {
		return agent.ErrUnknownFunctionCall
	}
	timer := time.NewTimer(terminalSendTimeout)
	defer timer.Stop()
	select {
	case s.out <- env:
		return nil
	case <-s.cancelCtx.Done():
		return s.cancelCtx.Err()
	case <-timer.C:
		return errors.New("function call delivery timed out")
	}
}

func (s *Session) SubmitFunctionResult(ctx context.Context, result proto.FunctionResultPayload) error {
	if s.functions == nil {
		return agent.ErrUnknownFunctionCall
	}
	s.functions.mu.Lock()
	defer s.functions.mu.Unlock()
	id, exists := s.functions.pending[result.CallID]
	if !exists || s.functions.closed || s.cancelled.Load() || s.terminal.Load() {
		return agent.ErrUnknownFunctionCall
	}
	if err := result.ValidateContent(); err != nil {
		return err
	}
	content := make([]functionContent, 0, len(result.Content))
	for _, part := range result.Content {
		kind := "inputText"
		if part.Type == "input_image" {
			kind = "inputImage"
		}
		content = append(content, functionContent{Type: kind, Text: part.Text, ImageURL: part.ImageURL})
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	reply := struct {
		Success      bool              `json:"success"`
		ContentItems []functionContent `json:"contentItems"`
	}{Success: result.Success, ContentItems: content}
	if err := s.rpc.writeFrameContext(ctx, JsonRpcResponse{JsonRpc: JsonRpcVersion, ID: id, Result: reply}); err != nil {
		return fmt.Errorf("write function result: %w", err)
	}
	delete(s.functions.pending, result.CallID)
	return nil
}

type functionContent struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL *string `json:"imageUrl,omitempty"`
}

func (s *Session) stopFunctionCalls() {
	if s.functions != nil {
		s.functions.mu.Lock()
		s.functions.closed = true
		clear(s.functions.pending)
		s.functions.mu.Unlock()
	}
}
