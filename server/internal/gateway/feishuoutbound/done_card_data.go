package feishuoutbound

// done_card_data.go bridges the store to gateway.BuildDoneCard. Two
// callers share this assembly: the initial DoneCard send (worker) and
// the inflight driver's final-card patch.

import (
	"context"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// doneCardAssemblyStore is the slice of *store.Store this helper needs.
type doneCardAssemblyStore interface {
	LoadDoneCardRunData(ctx context.Context, workspaceID, projectID, runID string) (store.DoneCardRunData, error)
	ListAgentRunEventsAfterSeq(ctx context.Context, runID string, afterSeq int64, limit int32) ([]store.AgentRunEvent, error)
	GetGuestReplyHintForRun(ctx context.Context, conversationID, runID string) (string, error)
}

// assembleDoneCardInput is the per-call context.
type assembleDoneCardInput struct {
	WorkspaceID string
	ProjectID   string
	RunID       string
	// PrefilledSteps overrides the agent_run_events read entirely when
	// non-nil. The driver passes prev.Payload steps directly.
	PrefilledSteps []gateway.StepInfo
	// PrefilledElapsed: when non-zero, skips the elapsed-half of the
	// agent_runs read (usage_logs read still happens).
	PrefilledElapsed time.Duration
	// PrefilledThinking: when PrefilledSteps is set, the helper does
	// not read events, so the driver MUST pass thinking here too.
	PrefilledThinking string
}

// assembleDoneCardOutput is the bundle handed to gateway.BuildDoneCard.
// Usage is nil when the run has no usage_logs rows yet.
type assembleDoneCardOutput struct {
	Steps    []gateway.StepInfo
	Thinking string
	Elapsed  time.Duration
	Usage    *gateway.UsageStats
	// AgentName from store.DoneCardRunData so the caller can stamp the
	// per-card title without an extra store call. Empty when binding
	// is missing / soft-deleted.
	AgentName string
}

// assembleDoneCardData reads run + usage + step events and rolls them
// into BuildDoneCard's input shape. On read error returns what we have
// so far so the caller renders a best-effort card.
func assembleDoneCardData(ctx context.Context, s doneCardAssemblyStore, in assembleDoneCardInput) (assembleDoneCardOutput, error) {
	out := assembleDoneCardOutput{
		Steps:    in.PrefilledSteps,
		Thinking: in.PrefilledThinking,
		Elapsed:  in.PrefilledElapsed,
	}

	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		// No run id → no usage, no steps. Return whatever was prefilled.
		return out, nil
	}

	data, err := s.LoadDoneCardRunData(ctx, in.WorkspaceID, in.ProjectID, runID)
	if err != nil {
		return out, err
	}
	out.AgentName = data.AgentName
	if out.Elapsed == 0 && !data.StartedAt.IsZero() {
		finish := data.FinishedAt
		if finish.IsZero() {
			finish = time.Now().UTC()
		}
		out.Elapsed = finish.Sub(data.StartedAt)
	}

	// Steps + thinking. When the driver pre-folds events into
	// prev.Payload it hands them via PrefilledSteps and we skip this
	// read entirely. Otherwise read agent_run_events and fold both
	// views off the same scan.
	if out.Steps == nil {
		// Generous cap. Renderer caps visible steps at 50 anyway.
		events, err := s.ListAgentRunEventsAfterSeq(ctx, runID, 0, 500)
		if err != nil {
			return out, err
		}
		toolEvents := make([]gateway.ToolCallEvent, 0, len(events))
		var thinking strings.Builder
		for _, ev := range events {
			toolEvents = append(toolEvents, gateway.ToolCallEvent{
				EventKind:  ev.EventKind,
				Payload:    ev.Payload,
				OccurredAt: ev.OccurredAt,
			})
			if ev.EventKind == "message.thinking" {
				if t, ok := ev.Payload["thinking"].(string); ok && t != "" {
					thinking.WriteString(t)
				}
			}
		}
		out.Steps = gateway.StepsFromToolCallEvents(toolEvents)
		if out.Thinking == "" {
			out.Thinking = thinking.String()
		}
	}

	// Usage footer. Unknown models leave ContextWindow at zero; the
	// renderer reads that as "no usage" and degrades to `Ns · N steps`
	// rather than rendering a misleading 0%/NaN%.
	if data.HasUsage {
		window := gateway.ContextWindowForModel(data.Model)
		if window > 0 {
			out.Usage = &gateway.UsageStats{
				CostUSD:       data.CostUSD,
				ContextUsed:   data.ContextUsedTokens,
				ContextWindow: window,
				Model:         data.Model,
			}
		}
	}

	return out, nil
}
