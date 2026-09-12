package execution

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type pendingInput struct {
	sequence int64
	text     string
	started  time.Time
	waiting  bool
}

type cancellationResult struct {
	ack proto.InteractionDecisionAckPayload
	err error
}

func requestCancellation(ctx context.Context, peer *gateway.Session, runID string) <-chan cancellationResult {
	out := make(chan cancellationResult, 1)
	go func() {
		id := "cancel:" + runID
		env, _ := proto.NewEnvelope(proto.TypePromptCancel, runID, proto.PromptCancelPayload{DeliveryID: id})
		ack, err := peer.SendAndWaitInteractionAck(ctx, env, id)
		out <- cancellationResult{ack: ack, err: err}
	}()
	return out
}

func send(ctx context.Context, peer *gateway.Session, kind, runID string, payload any) error {
	env, err := proto.NewEnvelope(kind, runID, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return peer.Send(ctx, env)
}

func abort(peer *gateway.Session, runID string) {
	_ = send(context.Background(), peer, proto.TypePromptCancel, runID, proto.PromptCancelPayload{})
}

func (d *Dispatcher) deliver(ctx context.Context, tenantID, sessionID string, peer *gateway.Session, request proto.PromptRequestPayload, first int64) (result Result, status string) {
	status = store.TurnFailed
	result.AppliedThrough = first
	subscription, err := peer.SubscribeDurable(request.RunID)
	if err != nil {
		result.ErrorCode = "device_disconnected"
		return
	}
	upstream := subscription.Events
	defer peer.Unsubscribe(request.RunID)
	defer func() {
		if status == store.TurnFailed {
			abort(peer, request.RunID)
		}
	}()
	journal := &journal{store: d.Store, tenant: tenantID, session: sessionID, turn: request.RunID, next: 1}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := journal.drain(upstream, &result); err != nil {
			result.ErrorCode, status = "event_persistence_failed", store.TurnFailed
		}
		if err := journal.flush(finishCtx); err != nil {
			result.ErrorCode, status = "event_persistence_failed", store.TurnFailed
		}
		if subscription.Err() != nil {
			result.ErrorCode, status = "event_stream_incomplete", store.TurnFailed
		}
	}()
	if err := send(ctx, peer, proto.TypePromptRequest, request.RunID, request); err != nil {
		result.ErrorCode = "delivery_unknown"
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	flushTicker := time.NewTicker(100 * time.Millisecond)
	defer flushTicker.Stop()
	var pending *pendingInput
	var cancelSent time.Time
	var cancelReply <-chan cancellationResult
	functions := &functionExchange{store: d.Store, tenant: tenantID, session: sessionID, turn: request.RunID, tools: request.FunctionTools}
	done := false
	cancelCtx, stopCancellation := context.WithCancel(ctx)
	defer stopCancellation()
	for {
		if done && functions.reply == nil {
			status = finishDelivery(ctx, journal, &result, cancelReply, pending != nil, functions)
			return
		}
		select {
		case reply := <-functions.reply:
			// Once cancellation is sent, its receipt owns the terminal outcome.
			if err := functions.confirm(ctx, reply); err != nil && cancelReply == nil {
				result.ErrorCode = "function_result_unconfirmed"
				return
			}
		case <-flushTicker.C:
			if journal.flush(ctx) != nil {
				result.ErrorCode = "event_persistence_failed"
				return
			}
		case reply := <-cancelReply:
			drainErr := journal.drain(upstream, &result)
			receiptErr := recordCancellation(ctx, journal, reply, &result)
			if drainErr != nil || receiptErr != nil {
				result.ErrorCode = "event_persistence_failed"
				return
			}
			if reply.err == nil && reply.ack.Applied {
				if reply.ack.Outcome == nil {
					result.ErrorCode = "cancel_outcome_unavailable"
					return
				}
				status = store.TurnCancelled
			} else {
				result.ErrorCode = "cancel_unconfirmed"
			}
			return
		case <-ctx.Done():
			result.ErrorCode = "execution_interrupted"
			return
		case env, ok := <-upstream:
			if !ok {
				result.ErrorCode = "device_disconnected"
				return
			}
			writeErr := journal.observe(ctx, env)
			if result.mergeObservation(env) != nil {
				result.ErrorCode = "invalid_executor_result"
				return
			}
			if writeErr != nil {
				result.ErrorCode = "event_persistence_failed"
				return
			}
			switch env.Type {
			case proto.TypeError:
				var failure proto.ErrorPayload
				if env.DecodePayload(&failure) != nil {
					result.ErrorCode = "invalid_executor_result"
					return
				}
				result.ErrorCode, result.Error = "engine_failed", failure.Error
			case proto.TypeDone:
				// Done closes the event subscription; application receipts have a separate waiter.
				done, upstream = true, nil
			case proto.TypeFunctionCall:
				if err := journal.flush(ctx); err != nil {
					result.ErrorCode = "event_persistence_failed"
					return
				}
				if err := functions.record(ctx, env); err != nil {
					result.ErrorCode = "function_call_invalid"
					return
				}
			case proto.TypePromptSteerAck:
				var ack proto.PromptSteerAckPayload
				if env.DecodePayload(&ack) != nil {
					result.ErrorCode = "invalid_executor_result"
					return
				}
				if pending == nil || ack.InputID != strconv.FormatInt(pending.sequence, 10) {
					continue
				}
				switch {
				case ack.Accepted:
					result.AppliedThrough = pending.sequence
					pending = nil
				case ack.ErrorCode == "not_ready" || ack.ErrorCode == "busy":
					pending.waiting = false
				case ack.ErrorCode == "in_flight":
				default:
					result.ErrorCode, result.Error = "input_"+ack.ErrorCode, ack.Error
					return
				}
			case proto.TypePermissionRequest, proto.TypePromptForUserChoice, proto.TypeAuthoringRequest:
				result.ErrorCode = "interaction_not_supported"
				return
			}
		case <-ticker.C:
			if !cancelSent.IsZero() {
				if time.Since(cancelSent) > 15*time.Second {
					result.ErrorCode = "cancel_unconfirmed"
					return
				}
				continue
			}
			turn, err := d.Store.GetTurn(ctx, tenantID, sessionID, request.RunID)
			if err != nil {
				result.ErrorCode = "execution_state_unavailable"
				return
			}
			if turn.Status != store.TurnInProgress && turn.Status != store.TurnWaiting {
				result.ErrorCode = "execution_state_changed"
				return
			}
			if !turn.CancelRequestedAt.IsZero() {
				cancelReply = requestCancellation(cancelCtx, peer, request.RunID)
				cancelSent = time.Now()
				continue
			}
			if err := functions.start(cancelCtx, peer); err != nil {
				result.ErrorCode = "function_result_invalid"
				return
			}
			if done {
				continue
			}
			if pending == nil {
				inputs, err := d.Store.ListTurnInputs(ctx, tenantID, sessionID, request.RunID, result.AppliedThrough, 1)
				if err != nil {
					result.ErrorCode = "execution_state_unavailable"
					return
				}
				if len(inputs) == 0 {
					continue
				}
				if inputs[0].Kind == "cancel" {
					continue
				}
				text, err := messageText(inputs[0].Payload)
				if err != nil || inputs[0].Kind != "message" {
					result.ErrorCode = "invalid_input"
					return
				}
				pending = &pendingInput{sequence: inputs[0].Sequence, text: text, started: time.Now()}
			}
			if time.Since(pending.started) > 30*time.Second {
				result.ErrorCode = "input_outcome_unknown"
				return
			}
			if !pending.waiting {
				if send(ctx, peer, proto.TypePromptSteer, request.RunID, proto.PromptSteerPayload{InputID: strconv.FormatInt(pending.sequence, 10), Text: pending.text}) != nil {
					result.ErrorCode = "input_outcome_unknown"
					return
				}
				pending.waiting = true
			}
		}
	}
}

func (r *Result) mergeObservation(env proto.Envelope) error {
	switch env.Type {
	case proto.TypeUsage:
		return env.DecodePayload(&r.Done.Usage)
	case proto.TypeDone:
		return r.mergeDone(env.Payload)
	}
	return nil
}

func (r *Result) mergeDone(raw json.RawMessage) error {
	var done proto.DonePayload
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &done); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if _, present := fields["usage"]; !present {
		done.Usage = r.Done.Usage
	}
	if done.Usage.Model == "" {
		done.Usage.Model = r.Done.Usage.Model
	}
	if done.Content == "" {
		done.Content = r.Done.Content
	}
	if done.Metadata == nil {
		done.Metadata = r.Done.Metadata
	}
	r.Done = done
	return nil
}
