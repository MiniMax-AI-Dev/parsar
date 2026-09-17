package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func daemonPreparedRemotePrompt(t *testing.T, ctx context.Context, peer *gateway.Session, req proto.PromptRequestPayload, cancelWhen func() bool) (proto.DonePayload, []proto.Envelope, *proto.InteractionDecisionAckPayload) {
	return daemonPreparedRemotePromptWithReady(t, ctx, peer, req, cancelWhen, nil, nil)
}

func daemonPreparedRemotePromptWithReady(t *testing.T, ctx context.Context, peer *gateway.Session, req proto.PromptRequestPayload, cancelWhen func() bool, onReady func(string) bool, onStarted func(string)) (proto.DonePayload, []proto.Envelope, *proto.InteractionDecisionAckPayload) {
	t.Helper()
	request := uuid.NewString()
	sub, err := peer.SubscribePreparation(request)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.UnsubscribePreparation(request)
	configuration := req
	configuration.RunID, configuration.Prompt = "", ""
	env, err := proto.NewEnvelope(proto.TypeExecutionPrepare, request, proto.ExecutionPreparePayload{Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
	var observations []proto.Envelope
	await := func(state string) proto.PreparationStatusPayload {
		t.Helper()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("real daemon preparation timed out")
			case event, ok := <-sub.Events:
				if !ok {
					t.Fatal("preparation closed before requested state", state, sub.Err())
				}
				observations = append(observations, event)
				var status proto.PreparationStatusPayload
				if event.DecodePayload(&status) != nil {
					t.Fatal("invalid preparation status")
				}
				if status.State == state || status.State == "failed" || status.State == "rejected" || status.State == "expired" {
					return status
				}
			}
		}
	}
	ready := await("ready")
	if ready.State != "ready" {
		return proto.DonePayload{}, observations, nil
	}
	if ready.Handle == "" || ready.Revision < 2 || ready.RunID != "" {
		t.Fatal("invalid pre-Turn ready identity")
	}
	if onReady != nil && !onReady(ready.Handle) {
		env, err := proto.NewEnvelope(proto.TypeExecutionRelease, request, proto.ExecutionReleasePayload{Handle: ready.Handle})
		if err != nil {
			t.Fatal(err)
		}
		if err = peer.Send(ctx, env); err != nil {
			t.Fatal(err)
		}
		released := await("released")
		if released.State != "released" || released.Handle != ready.Handle || released.Revision <= ready.Revision {
			t.Fatal("unused native preparation release failed")
		}
		return proto.DonePayload{}, observations, nil
	}
	start := func(run string) error {
		env, err := proto.NewEnvelope(proto.TypeExecutionStart, request, proto.ExecutionStartPayload{Handle: ready.Handle, RunID: run, Prompt: req.Prompt})
		if err != nil {
			return err
		}
		if err = peer.Send(ctx, env); err != nil {
			return err
		}
		started := await("started")
		if started.State != "started" || started.Handle != ready.Handle || started.RunID != run || started.Revision <= ready.Revision {
			return errors.New("prepared native transfer failed")
		}
		if onStarted != nil {
			onStarted(run)
		}
		return nil
	}
	done, events, ack := daemonRemotePromptWithStart(t, ctx, peer, req, cancelWhen, start)
	return done, append(observations, events...), ack
}

func daemonRemotePreparationFailed(events []proto.Envelope) bool {
	for _, event := range events {
		var status proto.PreparationStatusPayload
		if event.Type == proto.TypePreparationStatus && event.DecodePayload(&status) == nil && status.State == "failed" {
			return true
		}
	}
	return false
}
