package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func functionTools(raw []json.RawMessage) ([]proto.FunctionTool, error) {
	tools := make([]proto.FunctionTool, 0, len(raw))
	names := map[string]bool{}
	if len(raw) > 64 {
		return nil, errors.New("at most 64 function tools are supported")
	}
	for _, value := range raw {
		var tool struct {
			Type         string          `json:"type"`
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			Parameters   json.RawMessage `json:"parameters"`
			DeferLoading bool            `json:"defer_loading"`
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&tool); err != nil {
			return nil, err
		}
		var schema map[string]json.RawMessage
		if tool.Type != "function" || tool.DeferLoading || strings.TrimSpace(tool.Name) == "" || len(tool.Name) > 512 || names[tool.Name] || json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
			return nil, errors.New("execution requires unique non-deferred functions with object schemas")
		}
		names[tool.Name] = true
		tools = append(tools, proto.FunctionTool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	return tools, nil
}

type functionReply struct {
	ack proto.InteractionDecisionAckPayload
	err error
}

type functionExchange struct {
	store                 *store.Store
	tenant, session, turn string
	tools                 []proto.FunctionTool
	callID                string
	reply                 <-chan functionReply
}

func (f *functionExchange) record(ctx context.Context, env proto.Envelope) error {
	var call proto.FunctionCallPayload
	if env.ID != f.turn || env.DecodePayload(&call) != nil {
		return errors.New("invalid function callback")
	}
	declared := false
	for _, tool := range f.tools {
		declared = declared || tool.Name == call.Name
	}
	if !declared {
		return errors.New("undeclared function callback")
	}
	err := f.store.RecordFunctionCall(ctx, f.tenant, f.session, f.turn, store.FunctionCall{
		CallID: items.Identity(f.turn, "tool:"+call.CallID), ExecutorCallID: call.CallID, Name: call.Name, Arguments: call.Arguments,
	})
	return f.unlessCancelling(ctx, err)
}

func (f *functionExchange) start(ctx context.Context, peer *gateway.Session) error {
	if f.reply != nil || len(f.tools) == 0 {
		return nil
	}
	calls, err := f.store.PendingFunctionCalls(ctx, f.tenant, f.session, f.turn)
	if err != nil {
		return err
	}
	for _, call := range calls {
		if len(call.Result) == 0 {
			continue
		}
		result, err := functionResult(call)
		if err != nil {
			return err
		}
		env, err := proto.NewEnvelope(proto.TypeFunctionResult, f.turn, result)
		if err != nil {
			return err
		}
		replies := make(chan functionReply, 1)
		f.callID, f.reply = call.CallID, replies
		go func() {
			ack, err := peer.SendAndWaitInteractionAck(ctx, env, result.DeliveryID)
			replies <- functionReply{ack: ack, err: err}
		}()
		return nil
	}
	return nil
}

func (f *functionExchange) confirm(ctx context.Context, reply functionReply) error {
	id := f.callID
	f.callID, f.reply = "", nil
	if reply.err != nil {
		return reply.err
	}
	if !reply.ack.Applied {
		return fmt.Errorf("function result not applied: %s", reply.ack.ErrorCode)
	}
	return f.unlessCancelling(ctx, f.store.ConfirmFunctionResult(ctx, f.tenant, f.session, f.turn, id))
}

func (f *functionExchange) unlessCancelling(ctx context.Context, err error) error {
	if errors.Is(err, store.ErrTurnConflict) {
		turn, lookupErr := f.store.GetTurn(ctx, f.tenant, f.session, f.turn)
		if lookupErr == nil && !turn.CancelRequestedAt.IsZero() {
			return nil
		}
	}
	return err
}

func (f *functionExchange) complete(ctx context.Context) error {
	if len(f.tools) == 0 {
		return nil
	}
	calls, err := f.store.PendingFunctionCalls(ctx, f.tenant, f.session, f.turn)
	if err != nil {
		return err
	}
	if len(calls) != 0 {
		return errors.New("function calls remain unapplied")
	}
	turn, err := f.store.GetTurn(ctx, f.tenant, f.session, f.turn)
	if err != nil {
		return err
	}
	if turn.Status == store.TurnWaiting {
		return errors.New("function turn has not resumed")
	}
	return nil
}

func functionResult(call store.FunctionCall) (proto.FunctionResultPayload, error) {
	var value struct {
		Success *bool           `json:"success"`
		Output  json.RawMessage `json:"output"`
		Error   *string         `json:"error"`
	}
	if err := json.Unmarshal(call.Result, &value); err != nil {
		return proto.FunctionResultPayload{}, err
	}
	if value.Success == nil {
		return proto.FunctionResultPayload{}, errors.New("function result requires success")
	}
	result := proto.FunctionResultPayload{DeliveryID: "function:" + call.CallID, CallID: call.ExecutorCallID, Success: *value.Success, Content: []proto.FunctionResultContent{}}
	output := bytes.TrimSpace(value.Output)
	if len(output) > 0 && !bytes.Equal(output, []byte("null")) {
		if output[0] == '"' {
			var text string
			if err := json.Unmarshal(output, &text); err != nil {
				return result, err
			}
			result.Content = append(result.Content, proto.FunctionResultContent{Type: "input_text", Text: &text})
		} else if err := json.Unmarshal(output, &result.Content); err != nil {
			return result, err
		}
	}
	if value.Error != nil {
		result.Content = append(result.Content, proto.FunctionResultContent{Type: "input_text", Text: value.Error})
	}
	return result, result.ValidateContent()
}
