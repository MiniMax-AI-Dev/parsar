package dev

import (
	"context"
	"errors"
	"fmt"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type runStreamStore interface {
	GetAgentRunInvocation(ctx context.Context, runID string) (store.AgentRunInvocation, error)
	MarkAgentRunRunning(ctx context.Context, runID string, conversationID string) (store.MarkAgentRunRunningResult, error)
	RecordAgentRunEvent(ctx context.Context, input store.RecordAgentRunEventInput) error
	SendAssistantMessageFromRun(ctx context.Context, input store.SendAssistantMessageFromRunInput) (store.CompleteAgentRunResult, error)
}

// inflightChecker is the optional store interface for the fast-path
// "is a sibling already running" check. The slow-path NOT EXISTS guard
// inside MarkAgentRunRunning actually defends the race, so callers can
// treat a missing implementation as "no fast check".
type inflightChecker interface {
	HasInflightRunForConversationAgent(ctx context.Context, runID string) (bool, error)
}

// dispatchRunTimeout caps a single dispatched run. Safety net for runaway
// opencode sessions / network hangs, not a product-level latency budget.
const dispatchRunTimeout = 30 * time.Minute

// streamFirstEventTimeout protects /stream from hanging when no producer
// ever publishes (e.g. /start failed or runID is stale). After this window
// with zero events, refreshes run status: terminal → synthesize an error;
// still queued → emit timeout error.
//
// var (not const) so tests can shrink it; production never reassigns.
var streamFirstEventTimeout = 30 * time.Second

// StreamingDispatchDeps is the minimum slice of routerConfig needed to
// auto-start a conversation agent run from outside the dev package.
type StreamingDispatchDeps struct {
	Broker            *runstream.Broker
	ConnectorRegistry *connector.Registry
	DispatchCtx       context.Context
}

func (d StreamingDispatchDeps) routerConfig() *routerConfig {
	return &routerConfig{
		connectorRegistry: d.ConnectorRegistry,
		runBroker:         d.Broker,
		dispatchCtx:       d.DispatchCtx,
	}
}

// StartConversationRun is the imperative shape of the /start handler:
// flip queued → running, then spawn dispatchConversationRun.
//
// Exported so the store-side StreamingDispatcher (wired in cmd/server)
// can auto-start agent_daemon runs without going through HTTP. Returns
// the post-MarkAgentRunRunning status so callers can distinguish a
// fresh start (status=running) from "already running" (ErrAgentRunNotStartable).
func StartConversationRun(
	ctx context.Context,
	runtimeStore RuntimeStore,
	deps StreamingDispatchDeps,
	runID, conversationID string,
) (string, error) {
	if runtimeStore == nil {
		return "", errors.New("conversation run streaming runtime store is nil")
	}
	streamStore, ok := runtimeStore.(runStreamStore)
	if !ok {
		return "", errors.New("conversation run streaming store is not wired")
	}
	log.Bg().Info("StartConversationRun: entering",
		"run_id", runID, "conversation_id", conversationID)

	// Fast-path serial-queue check: if another run for the same
	// (conversation, agent) is already running, don't dispatch
	// this one. The fast-path is an optimization — MarkAgentRunRunning's
	// NOT EXISTS guard is the actual race defender.
	if checker, ok := runtimeStore.(inflightChecker); ok {
		inflight, checkErr := checker.HasInflightRunForConversationAgent(ctx, runID)
		if checkErr != nil {
			log.Bg().Warn("StartConversationRun: inflight check failed (continuing to slow path)",
				"run_id", runID, "conversation_id", conversationID, "err", checkErr)
		} else if inflight {
			log.Bg().Info("StartConversationRun: blocked by inflight sibling, staying queued",
				"run_id", runID, "conversation_id", conversationID)
			recordRunLifecycleEvent(streamStore, runID, "run.queued", map[string]any{
				"source":          "conversation_stream",
				"conversation_id": conversationID,
				"reason":          "inflight_sibling",
			}, time.Now().UTC())
			return "queued", nil
		}
	}

	started, err := streamStore.MarkAgentRunRunning(ctx, runID, conversationID)
	if err != nil {
		// Slow-path race: a sibling started between our fast-path
		// check and this UPDATE. Stay queued.
		if errors.Is(err, store.ErrAgentRunBlockedByQueue) {
			log.Bg().Info("StartConversationRun: blocked by inflight sibling (slow path), staying queued",
				"run_id", runID, "conversation_id", conversationID)
			recordRunLifecycleEvent(streamStore, runID, "run.queued", map[string]any{
				"source":          "conversation_stream",
				"conversation_id": conversationID,
				"reason":          "inflight_sibling_race",
			}, time.Now().UTC())
			return "queued", nil
		}
		log.Bg().Warn("StartConversationRun: MarkAgentRunRunning failed",
			"run_id", runID, "conversation_id", conversationID, "err", err.Error())
		return "", err
	}
	log.Bg().Info("StartConversationRun: marked running, spawning dispatchConversationRun",
		"run_id", runID, "conversation_id", conversationID, "status", started.Status)
	recordRunLifecycleEvent(streamStore, runID, "run.started", map[string]any{"source": "conversation_stream", "conversation_id": started.ConversationID, "status": started.Status}, started.StartedAt)
	cfg := deps.routerConfig()
	go dispatchConversationRun(dispatchParentCtx(cfg), runtimeStore, cfg, runID)
	return started.Status, nil
}

// startConversationAgentRun is POST
// /api/v1/conversations/{conversationID}/runs/{runID}/start. Flips a queued
// run to running and spawns the connector dispatcher goroutine.
//
//	@Summary		Start a queued agent run
//	@Description	Marks the run as running (or reports "queued" if a sibling run for the same conversation+agent is already inflight) and dispatches it to the connector. Callers should then subscribe to /stream for events.
//	@Tags			agent-runs
//	@ID				startDevConversationAgentRun
//	@Produce		json
//	@Param			conversationID	path		string					true	"Conversation UUID"
//	@Param			runID			path		string					true	"Agent run UUID"
//	@Success		200				{object}	map[string]interface{}	"Run already running — idempotent no-op"
//	@Success		202				{object}	map[string]interface{}	"Run dispatched (running) or held (queued)"
//	@Failure		400				{object}	map[string]string		"conversation_id or run_id is not a valid uuid"
//	@Failure		403				{object}	map[string]string		"Caller is not a workspace member or is viewer-only"
//	@Failure		404				{object}	map[string]string		"Run not found or not part of conversation"
//	@Failure		422				{object}	map[string]string		"Run status must be queued before start"
//	@Failure		500				{object}	map[string]string		"Failed to mark agent run running"
//	@Failure		503				{object}	map[string]string		"Database-backed conversation run streaming is disabled"
//	@Router			/api/v1/conversations/{conversationID}/runs/{runID}/start [post]
func startConversationAgentRun(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation run streaming is disabled"})
			return
		}
		conversationID, runID, ok := conversationRunParams(w, r)
		if !ok {
			return
		}
		run, ok := loadRunForConversation(w, r, runtimeStore, conversationID, runID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, run.WorkspaceID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		deps := StreamingDispatchDeps{
			Broker:            cfg.runBroker,
			ConnectorRegistry: cfg.connectorRegistry,
			DispatchCtx:       cfg.dispatchCtx,
		}
		status, err := StartConversationRun(r.Context(), runtimeStore, deps, runID, conversationID)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgentRun):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrAgentRunNotStartable):
				// MarkAgentRunRunning collapses running and terminal
				// into the same error. Server-side auto-start means a
				// frontend /start arriving second commonly hits a
				// running run — that's an idempotent no-op.
				if cur, getErr := runtimeStore.GetAgentRun(r.Context(), runID); getErr == nil && cur.Status == "running" {
					writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "status": "running"})
					return
				}
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "run status must be queued before start"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mark agent run running"})
			}
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"run_id": runID, "status": status})
	}
}

// streamConversationAgentRun is GET
// /api/v1/conversations/{conversationID}/runs/{runID}/stream. Server-Sent
// Events endpoint that forwards the connector's PromptEvents to the
// browser.
//
//	@Summary		Stream agent run events (SSE)
//	@Description	Server-Sent Events stream that forwards the connector's PromptEvents (deltas, tool calls, permission requests, final message) for the run. Content-Type is text/event-stream; each event's data field is a JSON-encoded PromptEvent.
//	@Tags			agent-runs
//	@ID				streamDevConversationAgentRun
//	@Produce		text/event-stream
//	@Param			conversationID	path	string	true	"Conversation UUID"
//	@Param			runID			path	string	true	"Agent run UUID"
//	@Success		200				"SSE stream opened"
//	@Failure		400				{object}	map[string]string	"conversation_id or run_id is not a valid uuid"
//	@Failure		403				{object}	map[string]string	"Caller is not a workspace member or is viewer-only"
//	@Failure		404				{object}	map[string]string	"Run not found or not part of conversation"
//	@Failure		500				{object}	map[string]string	"ResponseWriter does not support flushing"
//	@Failure		503				{object}	map[string]string	"Database-backed conversation run streaming is disabled"
//	@Router			/api/v1/conversations/{conversationID}/runs/{runID}/stream [get]
func streamConversationAgentRun(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation run streaming is disabled"})
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ResponseWriter does not support flushing; SSE requires http.Flusher"})
			return
		}
		conversationID, runID, ok := conversationRunParams(w, r)
		if !ok {
			return
		}
		run, ok := loadRunForConversation(w, r, runtimeStore, conversationID, runID)
		if !ok {
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, run.WorkspaceID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		broker := runBroker(cfg)
		events := broker.Subscribe(r.Context(), runID)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Hang protection: if the producer never publishes, wait at
		// most streamFirstEventTimeout for the first event. If nothing
		// arrives, refresh run status to decide whether to synthesize
		// an error frame and return.
		firstEventTimer := time.NewTimer(streamFirstEventTimeout)
		defer firstEventTimer.Stop()
		var sawFirst bool
		for {
			if sawFirst {
				ev, ok := <-events
				if !ok {
					return
				}
				if err := writeSSEEvent(w, ev); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				if err := writeSSEEvent(w, ev); err != nil {
					return
				}
				flusher.Flush()
				sawFirst = true
			case <-r.Context().Done():
				return
			case <-firstEventTimer.C:
				// A queued run can legitimately sit unbounded while
				// an older sibling finishes. Re-arm the timer and
				// keep waiting silently in that case.
				if run, err := runtimeStore.GetAgentRun(r.Context(), runID); err == nil && run.Status == "queued" {
					firstEventTimer.Reset(streamFirstEventTimeout)
					continue
				}
				writeStreamHangError(w, flusher, r.Context(), runtimeStore, runID)
				return
			}
		}
	}
}

// writeStreamHangError emits a synthesized error frame when /stream hit
// the first-event deadline. Refreshes run status to give the UI a useful
// reason.
//
// The "run was cancelled..." reason carries a stable "run_cancelled:"
// prefix that the web UI matches to suppress the red "Stream output interrupted"
// banner. See useAgentRunStream.onError in apps/web/src/lib/api-conversations.ts —
// don't reword without updating the frontend matcher.
func writeStreamHangError(w http.ResponseWriter, flusher http.Flusher, ctx context.Context, runtimeStore RuntimeStore, runID string) {
	reason := "stream timed out waiting for dispatcher; check run status"
	if run, err := runtimeStore.GetAgentRun(ctx, runID); err == nil {
		switch run.Status {
		case "failed":
			reason = "run failed before producing any stream event"
		case "completed":
			reason = "run completed before subscriber attached; refetch timeline for final message"
		case "queued":
			reason = "stream timed out: dispatcher never started; retry from the composer"
		case "cancelled":
			reason = "run_cancelled: run was cancelled before producing any stream event"
		}
	}
	_ = writeSSEEvent(w, connector.PromptEvent{Type: connector.EventError, Error: reason})
	flusher.Flush()
}

func dispatchConversationRun(ctx context.Context, runtimeStore RuntimeStore, cfg *routerConfig, runID string) {
	// Per-run deadline so a runaway opencode session can't hold the
	// dispatch goroutine indefinitely. cancel is idempotent; defer
	// after Finish so the broker is closed before the context.
	log.Bg().Info("dispatchConversationRun: enter", "run_id", runID)
	ctx, cancel := context.WithTimeout(ctx, dispatchRunTimeout)
	defer cancel()
	broker := runBroker(cfg)
	streamStore, ok := runtimeStore.(runStreamStore)
	if !ok {
		log.Bg().Error("dispatchConversationRun: stream store not wired", "run_id", runID)
		publishAndFailRun(ctx, runtimeStore, broker, runID, "conversation_stream", fmt.Errorf("conversation run streaming store is not wired"))
		return
	}
	defer broker.Finish(runID)
	invocation, err := streamStore.GetAgentRunInvocation(ctx, runID)
	if err != nil {
		log.Bg().Warn("dispatchConversationRun: GetAgentRunInvocation failed", "run_id", runID, "err", err.Error())
		publishAndFailRun(ctx, runtimeStore, broker, runID, "conversation_stream", err)
		return
	}
	log.Bg().Info("dispatchConversationRun: loaded invocation",
		"run_id", runID,
		"connector_type", invocation.ConnectorType,
		"agent_id", invocation.AgentID,
		"conversation_id", invocation.ConversationID,
		"agent_name", invocation.AgentName)
	in := connector.PromptInput{
		RunID:                   invocation.RunID,
		WorkspaceID:             invocation.WorkspaceID,
		ConversationID:          invocation.ConversationID,
		AgentID:                 invocation.AgentID,
		AgentName:               invocation.AgentName,
		AgentSlug:               invocation.AgentSlug,
		ConversationInitiatorID: userConversationInitiatorID(invocation),
		TriggerMessageContent:   invocation.TriggerMessageContent,
		TriggerAttachments:      invocation.TriggerAttachments,
		AgentConfig:             invocation.AgentConfig,
	}
	source := invocation.ConnectorType
	target := resolveStreamConnector(cfg, invocation.ConnectorType)
	if target == nil {
		log.Bg().Error("dispatchConversationRun: no AgentConnector registered for connector_type",
			"run_id", runID, "connector_type", invocation.ConnectorType)
		publishAndFailRun(ctx, runtimeStore, broker, runID, source,
			fmt.Errorf("connector_type %q is not registered for streaming dispatch", invocation.ConnectorType))
		return
	}
	log.Bg().Info("dispatchConversationRun: calling target.StreamPrompt",
		"run_id", runID, "connector_type", invocation.ConnectorType, "target_type", target.Type())
	events, err := target.StreamPrompt(ctx, in)
	if err != nil {
		log.Bg().Error("dispatchConversationRun: StreamPrompt returned error",
			"run_id", runID, "connector_type", invocation.ConnectorType, "err", err.Error())
		publishAndFailRun(ctx, runtimeStore, broker, runID, source, err)
		return
	}
	log.Bg().Info("dispatchConversationRun: streaming events from connector",
		"run_id", runID, "connector_type", invocation.ConnectorType)
	var final *connector.PromptOutput
	var streamErr string
	var sawFinalFailure bool
	for ev := range events {
		broker.Publish(runID, ev)
		recordPromptEvent(ctx, streamStore, runID, ev)
		if ev.Type == connector.EventDone {
			final = ev.Final
			if ev.Final == nil || strings.TrimSpace(ev.Final.Content) == "" {
				sawFinalFailure = true
			}
		}
		if ev.Type == connector.EventError {
			streamErr = ev.Error
		}
	}
	log.Bg().Info("dispatchConversationRun: event channel drained",
		"run_id", runID, "stream_err", streamErr, "has_final", final != nil)
	if streamErr != "" {
		// In-band failure: connector emitted EventError or empty Done.
		// recordPromptEvent already wrote session.error and/or
		// run.failed lifecycle events as the frames streamed.
		// failRunWithVisibleMessage here would double-emit run.failed.
		reason := streamErr
		if !sawFinalFailure {
			// Connector reported an error but never closed with
			// EventDone; backfill run.failed so the event stream
			// still terminates cleanly.
			recordRunLifecycleEvent(streamStore, runID, "run.failed", map[string]any{"source": source, "error": reason}, time.Now().UTC())
		}
		failRunRowOnly(ctx, runtimeStore, runID, source, reason)
		return
	}
	persistFinal(ctx, runtimeStore, streamStore, runID, source, final)
}

// resolveStreamConnector picks the AgentConnector for a run's
// connector_type from the shared registry.
func resolveStreamConnector(cfg *routerConfig, connectorType string) connector.AgentConnector {
	if cfg == nil || cfg.connectorRegistry == nil {
		return nil
	}
	if conn, err := cfg.connectorRegistry.Get(connectorType); err == nil {
		return conn
	}
	return nil
}

func userConversationInitiatorID(invocation store.AgentRunInvocation) string {
	if invocation.RequestedByType != "user" {
		return ""
	}
	return strings.TrimSpace(invocation.RequestedByID)
}

func recordPromptEvent(ctx context.Context, streamStore runStreamStore, runID string, ev connector.PromptEvent) {
	kind, payload, ok := eventPersistencePayload(ev)
	if !ok {
		return
	}
	recordRunLifecycleEvent(streamStore, runID, kind, payload, time.Now().UTC())
}

func eventPersistencePayload(ev connector.PromptEvent) (string, map[string]any, bool) {
	switch ev.Type {
	case connector.EventDelta:
		return "message.delta", map[string]any{"delta": ev.Delta, "sequence": ev.Sequence}, true
	case connector.EventThinking:
		return "message.thinking", map[string]any{"thinking": ev.Thinking, "sequence": ev.Sequence}, true
	case connector.EventToolCall:
		if ev.Tool == nil {
			return "tool.call", map[string]any{"sequence": ev.Sequence}, true
		}
		kind := "tool.call"
		if ev.Tool.Stage == "after" {
			kind = "tool.result"
		}
		return kind, map[string]any{"id": ev.Tool.ID, "name": ev.Tool.Name, "stage": ev.Tool.Stage, "args": ev.Tool.Args, "result": ev.Tool.Result, "sequence": ev.Sequence}, true
	case connector.EventPermissionRequest:
		if ev.Permission == nil {
			return "permission.asked", map[string]any{"sequence": ev.Sequence}, true
		}
		return "permission.asked", map[string]any{"request_id": ev.Permission.ID, "device_id": ev.Permission.DeviceID, "action": ev.Permission.Tool, "resource": ev.Permission.Title, "detail": ev.Permission.Detail, "payload": ev.Permission.Payload, "sequence": ev.Sequence}, true
	case connector.EventPromptForUserChoice:
		if ev.PromptForUserChoice == nil {
			return "prompt_for_user_choice.asked", map[string]any{"sequence": ev.Sequence}, true
		}
		// Walk every question (multi-question support). EffectiveQuestions
		// folds legacy single-question payloads into the same shape so
		// downstream readers only need one code path.
		qList := ev.PromptForUserChoice.EffectiveQuestions()
		questionsOut := make([]map[string]any, 0, len(qList))
		for _, q := range qList {
			opts := make([]map[string]any, 0, len(q.Options))
			for _, opt := range q.Options {
				opts = append(opts, map[string]any{"label": opt.Label, "description": opt.Description})
			}
			questionsOut = append(questionsOut, map[string]any{
				"id":           q.ID,
				"header":       q.Header,
				"question":     q.Question,
				"multi_select": q.MultiSelect,
				"options":      opts,
			})
		}
		payload := map[string]any{
			"request_id":  ev.PromptForUserChoice.ID,
			"device_id":   ev.PromptForUserChoice.DeviceID,
			"questions":   questionsOut,
			"tool_use_id": ev.PromptForUserChoice.ToolUseID,
			"sequence":    ev.Sequence,
		}
		// Mirror the first question into the legacy top-level fields so
		// rollback-compatible readers (older outbound code paths) still
		// see a card. Safe even when len(qList) == 0 — both branches are
		// no-ops then.
		if len(qList) > 0 {
			payload["question"] = qList[0].Question
			payload["header"] = qList[0].Header
			payload["multi_select"] = qList[0].MultiSelect
			legacyOpts := make([]map[string]any, 0, len(qList[0].Options))
			for _, opt := range qList[0].Options {
				legacyOpts = append(legacyOpts, map[string]any{"label": opt.Label, "description": opt.Description})
			}
			payload["options"] = legacyOpts
		}
		return "prompt_for_user_choice.asked", payload, true
	case connector.EventError:
		return "session.error", map[string]any{"error": ev.Error, "sequence": ev.Sequence}, true
	case connector.EventDone:
		if ev.Final != nil && strings.TrimSpace(ev.Final.Content) != "" {
			return "run.completed", map[string]any{"sequence": ev.Sequence}, true
		}
		errText := "empty final output"
		var source string
		if ev.Final != nil {
			if v := stringFromMap(ev.Final.Metadata, "error"); v != "" {
				errText = v
			}
			if v := stringFromMap(ev.Final.Metadata, "source"); v != "" {
				source = v
			}
		}
		payload := map[string]any{
			"sequence":             ev.Sequence,
			"error":                errText,
			"user_visible_message": store.MapUserFacingReason(errText),
		}
		if source != "" {
			payload["source"] = source
		}
		return "run.failed", payload, true
	default:
		return "", nil, false
	}
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func persistFinal(ctx context.Context, runtimeStore RuntimeStore, streamStore runStreamStore, runID string, source string, final *connector.PromptOutput) {
	if final == nil {
		final = &connector.PromptOutput{Content: ""}
	}
	_, err := streamStore.SendAssistantMessageFromRun(ctx, store.SendAssistantMessageFromRunInput{RunID: runID, Source: source, Content: final.Content, Transcript: final.Transcript, Usage: final.Usage})
	if err != nil {
		log.Bg().Warn("persist streamed agent final failed", "run_id", runID, "error", err)
		// failRunWithVisibleMessage records run.failed internally.
		failRunWithVisibleMessage(ctx, runtimeStore, runID, source, err.Error())
	}
}

// failRunWithVisibleMessage updates the run row to failed and emits a
// run.failed lifecycle event carrying the user-visible message. Used by
// out-of-band failure paths where the connector never emitted EventDone.
//
// The in-band path (EventError / empty EventDone) goes through
// eventPersistencePayload + failRunRowOnly to avoid double-emit.
func failRunWithVisibleMessage(ctx context.Context, runtimeStore RuntimeStore, runID string, source string, reason string) {
	if err := runtimeStore.FailAgentRun(ctx, store.FailAgentRunInput{RunID: runID, Source: source, Reason: reason}); err != nil {
		log.Bg().Warn("failed to mark streamed agent run failed", "run_id", runID, "error", err)
		return
	}
	// The Feishu inflight driver folds run.failed into its slot payload
	// and renders the terminal ErrorCard directly via PATCH. Web UI
	// consumers see the same failure via run.status='failed' + EventError.
	recorder, ok := runtimeStore.(runStreamStore)
	if !ok {
		return
	}
	recordRunLifecycleEvent(recorder, runID, "run.failed", map[string]any{
		"source":               source,
		"error":                reason,
		"user_visible_message": store.MapUserFacingReason(reason),
	}, time.Now().UTC())
}

// failRunRowOnly flips the run row to failed without emitting a run.failed
// lifecycle event. Used by the in-band stream-error path where
// eventPersistencePayload has already emitted run.failed from the
// connector's terminal frame.
func failRunRowOnly(ctx context.Context, runtimeStore RuntimeStore, runID string, source string, reason string) {
	if err := runtimeStore.FailAgentRun(ctx, store.FailAgentRunInput{RunID: runID, Source: source, Reason: reason}); err != nil {
		log.Bg().Warn("failed to mark streamed agent run failed", "run_id", runID, "error", err)
	}
}

func publishAndFailRun(ctx context.Context, runtimeStore RuntimeStore, broker interface {
	Publish(string, connector.PromptEvent)
}, runID string, source string, err error) {
	msg := err.Error()
	errorEvent := connector.PromptEvent{Type: connector.EventError, Error: msg}
	doneEvent := connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{Content: "", Metadata: map[string]any{"source": source, "error": msg}}}
	broker.Publish(runID, errorEvent)
	broker.Publish(runID, doneEvent)
	if streamStore, ok := runtimeStore.(runStreamStore); ok {
		// EventDone-empty already records run.failed via
		// eventPersistencePayload; use failRunRowOnly to avoid a
		// duplicate event row.
		recordPromptEvent(ctx, streamStore, runID, errorEvent)
		recordPromptEvent(ctx, streamStore, runID, doneEvent)
	}
	failRunRowOnly(ctx, runtimeStore, runID, source, msg)
}

func conversationRunParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
	if !isUUID(conversationID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
		return "", "", false
	}
	runID := strings.TrimSpace(chi.URLParam(r, "runID"))
	if !isUUID(runID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
		return "", "", false
	}
	return conversationID, runID, true
}

func loadRunForConversation(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, conversationID string, runID string) (store.AgentRunDetailRead, bool) {
	run, err := runtimeStore.GetAgentRun(r.Context(), runID)
	if err != nil {
		writeReadError(w, err, "failed to get agent run")
		return store.AgentRunDetailRead{}, false
	}
	if run.ConversationID != conversationID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent run does not belong to conversation"})
		return store.AgentRunDetailRead{}, false
	}
	return run, true
}

func runBroker(cfg *routerConfig) *runstream.Broker {
	if cfg != nil && cfg.runBroker != nil {
		return cfg.runBroker
	}
	return runstream.NewBroker(runstream.DefaultBufferSize)
}

// dispatchParentCtx returns the parent context for run dispatch
// goroutines. cmd/server wires WithDispatchContext(serverRootCtx) so
// SIGINT/SIGTERM cancel in-flight runs. Tests default to context.Background().
func dispatchParentCtx(cfg *routerConfig) context.Context {
	if cfg != nil && cfg.dispatchCtx != nil {
		return cfg.dispatchCtx
	}
	return context.Background()
}
