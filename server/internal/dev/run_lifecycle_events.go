package dev

import (
	"context"

	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type runLifecycleEventRecorder interface {
	RecordAgentRunEvent(ctx context.Context, input store.RecordAgentRunEventInput) error
}

func recordRunLifecycleEvent(recorder runLifecycleEventRecorder, runID string, eventKind string, payload map[string]any, occurredAt time.Time) {
	if recorder == nil || runID == "" || eventKind == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := recorder.RecordAgentRunEvent(writeCtx, store.RecordAgentRunEventInput{RunID: runID, EventKind: eventKind, Payload: payload, OccurredAt: occurredAt.UTC()}); err != nil {
			log.Bg().Warn("record agent run lifecycle event failed", "run_id", runID, "event_kind", eventKind, "error", err)
		}
	}()
}
