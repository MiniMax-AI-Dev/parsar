package dev

import (
	"context"
	"encoding/json"
	"errors"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type cancelAgentRunBody struct {
	Reason string `json:"reason"`
}

func cancelAgentRun(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed run cancel is disabled"})
			return
		}
		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}
		var body cancelAgentRunBody
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			reason = "user_clicked_cancel"
		}

		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, run.ProjectID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		if isTerminalRunStatus(run.Status) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "run already finished", "current_status": run.Status})
			return
		}

		ok, err := runtimeStore.CancelAgentRun(r.Context(), run.ID, reason)
		if err != nil {
			writeReadError(w, err, "failed to cancel agent run")
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, map[string]string{"status": "already_cancelling", "run_id": run.ID})
			return
		}

		sandboxAbortOK := true
		abortAttempted := false
		if target := resolveCancelConnector(cfg, run); target != nil {
			abortAttempted = true
			if err := target.Abort(r.Context(), connector.AbortInput{ConversationID: run.ConversationID, RunID: run.ID}); err != nil {
				sandboxAbortOK = false
				log.Bg().Warn("connector abort during run cancel failed", "run_id", run.ID, "conversation_id", run.ConversationID, "connector_type", run.ConnectorType, "error", err)
			}
		}
		if recorder, ok := runtimeStore.(runLifecycleEventRecorder); ok {
			recordRunLifecycleEvent(recorder, run.ID, "run.cancelled", map[string]any{"source": "admin_cancel", "reason": reason, "connector_type": run.ConnectorType, "abort_attempted": abortAttempted, "abort_ok": sandboxAbortOK}, time.Now().UTC())
		}
		emitCancelAudit(cfg, r, run, reason, sandboxAbortOK)
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "run_id": run.ID})
	}
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func resolveCancelConnector(cfg *routerConfig, run store.AgentRunDetailRead) connector.AgentConnector {
	if cfg == nil || cfg.connectorRegistry == nil {
		return nil
	}
	if conn, err := cfg.connectorRegistry.Get(run.ConnectorType); err == nil {
		return conn
	}
	return nil
}

func emitCancelAudit(cfg *routerConfig, r *http.Request, run store.AgentRunDetailRead, reason string, sandboxAbortOK bool) {
	if cfg == nil || cfg.auditIngester == nil {
		return
	}
	_ = cfg.auditIngester.Emit(audit.Event{
		OccurredAt:  time.Now().UTC(),
		Source:      audit.SourceRuntime,
		EventType:   store.AuditAgentRunCancelled,
		ActorType:   audit.ActorTypeUser,
		ActorID:     actorIDFromRequest(r),
		TargetType:  "agent_run",
		TargetID:    run.ID,
		WorkspaceID: run.WorkspaceID,
		ProjectID:   run.ProjectID,
		Payload: map[string]any{
			"reason":           reason,
			"sandbox_abort_ok": sandboxAbortOK,
		},
	})
}

// cancelConversationRuns handles POST /conversations/{conversationID}/cancel-all.
// One-shot bulk cancel for the conversation: every queued / running
// run is marked cancelled and the connector is told to Abort each one.
// Used by the "取消全部" button on the web conversation header and by
// the Feishu /cancel all command.
func cancelConversationRuns(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed run cancel is disabled"})
			return
		}
		convID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(convID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		var body cancelAgentRunBody
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			reason = "user_cancel_all"
		}

		conv, err := runtimeStore.GetProjectConversation(r.Context(), convID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, conv.ProjectID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}

		bulkCanceller, ok := runtimeStore.(conversationBulkCanceller)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bulk cancel not supported by current store"})
			return
		}
		cancelled, err := bulkCanceller.CancelAllInflightForConversation(r.Context(), convID, reason)
		if err != nil {
			writeReadError(w, err, "failed to bulk cancel runs")
			return
		}

		// Best-effort Abort + lifecycle event per cancelled run.
		for _, c := range cancelled {
			if target := resolveStreamConnector(cfg, c.ConnectorType); target != nil {
				if abortErr := target.Abort(r.Context(), connector.AbortInput{ConversationID: convID, RunID: c.ID}); abortErr != nil {
					log.Bg().Warn("connector abort during bulk cancel failed",
						"run_id", c.ID, "conversation_id", convID,
						"connector_type", c.ConnectorType, "error", abortErr)
				}
			}
			if recorder, ok := runtimeStore.(runLifecycleEventRecorder); ok {
				recordRunLifecycleEvent(recorder, c.ID, "run.cancelled", map[string]any{
					"source": "bulk_cancel", "reason": reason,
					"connector_type": c.ConnectorType,
				}, time.Now().UTC())
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "cancelled",
			"conversation_id": convID,
			"cancelled_count": len(cancelled),
		})
	}
}

// conversationBulkCanceller is the optional store interface used by
// cancelConversationRuns. *store.Store satisfies it; tests that use a
// lighter RuntimeStore mock can omit it and get a 503.
type conversationBulkCanceller interface {
	CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]store.SupersededRun, error)
}
