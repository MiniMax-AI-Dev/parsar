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
	if err := persistRunLifecycleEvent(context.Background(), recorder, runID, eventKind, payload, occurredAt); err != nil {
		log.Bg().Warn("record agent run lifecycle event failed", "run_id", runID, "event_kind", eventKind, "error", err)
	}
}

// persistRunLifecycleEvent writes synchronously. The canonical event row and
// any interaction derived from it must exist before callers publish a card or
// continue to a later lifecycle event; a detached best-effort goroutine can
// reorder terminal and interaction events or lose the only approval record.
func persistRunLifecycleEvent(ctx context.Context, recorder runLifecycleEventRecorder, runID string, eventKind string, payload map[string]any, occurredAt time.Time) error {
	if recorder == nil || runID == "" || eventKind == "" {
		return nil
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return recorder.RecordAgentRunEvent(writeCtx, store.RecordAgentRunEventInput{RunID: runID, EventKind: eventKind, Payload: payload, OccurredAt: occurredAt.UTC()})
}
