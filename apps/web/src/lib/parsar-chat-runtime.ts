/**
 * Parsar Chat Runtime — bridges the existing timeline API + SSE stream
 * into assistant-ui's ExternalStoreRuntime.
 *
 * Responsibilities:
 * 1. Convert ConversationTimelineMessage + runs → ThreadMessageLike[]
 * 2. Manage SSE subscription for streaming (delta/tool/thinking/done/error)
 * 3. Expose isRunning / onNew / onCancel to assistant-ui
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  useExternalStoreRuntime,
  type ThreadMessageLike,
  type AppendMessage,
} from "@assistant-ui/react"
import type {
  ConversationTimelineMessage,
  ConversationTimelineRun,
  SendUserMessageResponse,
  AgentRunStreamEvent,
  StreamToolEvent,
  ToolStep,
} from "./api-types"
import { startAgentRun } from "./api-conversations"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ParsarRuntimeConfig {
  conversationId: string
  workspaceId: string | null
  /** Timeline messages from React Query cache */
  timelineMessages: ConversationTimelineMessage[]
  /** Timeline runs from React Query cache */
  timelineRuns: ConversationTimelineRun[]
  /** Whether the timeline query is still loading */
  timelineLoading: boolean
  /** Send a user message — should call POST /messages API */
  onSendMessage: (content: string) => Promise<SendUserMessageResponse>
  /** Cancel all runs in this conversation */
  onCancelAll: () => void
  /** Cancel a specific run */
  onCancelRun: (runId: string) => void
  /** Invalidate timeline query cache (trigger refetch) */
  invalidateTimeline: () => void
  /** Called when a /start POST fails (surface as toast) */
  onStartError?: (message: string) => void
}

/** Streaming step tracked during an active SSE subscription */
export interface StreamingToolStep {
  tool_call_id: string
  name: string
  status: "running" | "completed"
  args?: Record<string, unknown>
  started_at: number
  ended_at?: number
}

interface StreamState {
  runId: string | null
  deltaText: string
  thinkingText: string
  steps: StreamingToolStep[]
  pendingInteraction: { kind: "permission" | "user_choice"; requestId: string } | null
  error: string | null
}

const IDLE_STREAM: StreamState = {
  runId: null,
  deltaText: "",
  thinkingText: "",
  steps: [],
  pendingInteraction: null,
  error: null,
}

// ---------------------------------------------------------------------------
// SSE helpers
// ---------------------------------------------------------------------------

function parseSSEData(type: string, raw: string): AgentRunStreamEvent | null {
  try {
    const data = JSON.parse(raw) as Record<string, unknown>
    return { ...data, type } as AgentRunStreamEvent
  } catch {
    return null
  }
}

function isCompletedBeforeSubscribeError(message: string): boolean {
  return message.includes("run completed before subscriber attached")
}

function isUserCancelledError(message: string): boolean {
  return message.startsWith("run_cancelled:")
}

// ---------------------------------------------------------------------------
// Message conversion: Timeline → ThreadMessageLike
// ---------------------------------------------------------------------------

export function convertTimelineMessage(
  msg: ConversationTimelineMessage,
  runs: ConversationTimelineRun[],
): ThreadMessageLike {
  if (msg.sender_type === "user") {
    return {
      id: msg.id,
      role: "user",
      content: [{ type: "text" as const, text: msg.content }],
      createdAt: new Date(msg.created_at),
    }
  }

  // --- Assistant message ---
  const parts: Array<
    | { readonly type: "text"; readonly text: string }
    | { readonly type: "reasoning"; readonly text: string }
    | { readonly type: "tool-call"; readonly toolCallId: string; readonly toolName: string; readonly args: Record<string, unknown>; readonly result?: unknown }
  > = []

  // Find associated runs (linked by output_message_id)
  const associatedRuns = runs.filter((r) => r.output_message_id === msg.id)

  // Add tool-call parts from run steps
  for (const run of associatedRuns) {
    for (const step of run.steps ?? []) {
      const status = deriveToolStatus(step, run.status)
      parts.push({
        type: "tool-call" as const,
        toolCallId: step.tool_call_id,
        toolName: step.name,
        args: step.args ?? {},
        result: status === "running" ? undefined : (step.result ?? { _empty: true }),
      })
    }
  }

  // Add text content (always last — matches Claude/Codex where text follows tool calls)
  if (msg.content) {
    parts.push({ type: "text" as const, text: msg.content })
  }

  // If no parts at all, add an empty text so assistant-ui doesn't complain
  if (parts.length === 0) {
    parts.push({ type: "text" as const, text: "" })
  }

  // runtime_error → mark as error status
  // Include error metadata in a structured format the UI can parse.
  // Encoding contract: \x00RUNTIME_ERROR\x00{metadataJSON}\x00{errorText}
  // This tunnels structured data through assistant-ui's text-only content
  // model. AssistantTextPart in ParsarThread detects the \x00 prefix and
  // routes to RuntimeErrorCard. Safe because API strings never contain
  // null bytes. If this becomes fragile, switch to a Map<messageId, metadata>
  // side-channel.
  if (msg.kind === "runtime_error") {
    // Replace the plain text part with one that includes metadata marker
    const errorText = msg.content || ""
    const metaJson = JSON.stringify(msg.metadata ?? {})
    const enrichedParts: typeof parts = [
      {
        type: "text" as const,
        text: `\x00RUNTIME_ERROR\x00${metaJson}\x00${errorText}`,
      },
    ]
    return {
      id: msg.id,
      role: "assistant",
      content: enrichedParts as ThreadMessageLike["content"],
      status: { type: "incomplete", reason: "error" },
      createdAt: new Date(msg.created_at),
    }
  }

  return {
    id: msg.id,
    role: "assistant",
    content: parts as ThreadMessageLike["content"],
    createdAt: new Date(msg.created_at),
  }
}

function deriveToolStatus(
  step: ToolStep,
  runStatus: string | undefined,
): "running" | "completed" | "failed" {
  if (step.status === "completed") return "completed"
  if (runStatus === "failed") return "failed"
  return step.status
}

// ---------------------------------------------------------------------------
// Build streaming assistant message from SSE state
// ---------------------------------------------------------------------------

function buildStreamingMessage(stream: StreamState): ThreadMessageLike | null {
  if (!stream.runId) return null
  const hasContent =
    stream.deltaText.length > 0 ||
    stream.thinkingText.length > 0 ||
    stream.steps.length > 0

  if (!hasContent) return null

  const parts: Array<
    | { readonly type: "text"; readonly text: string }
    | { readonly type: "reasoning"; readonly text: string }
    | { readonly type: "tool-call"; readonly toolCallId: string; readonly toolName: string; readonly args: Record<string, unknown>; readonly result?: unknown }
  > = []

  // Reasoning/thinking part
  if (stream.thinkingText) {
    parts.push({ type: "reasoning" as const, text: stream.thinkingText })
  }

  // Tool-call parts
  for (const step of stream.steps) {
    parts.push({
      type: "tool-call" as const,
      toolCallId: step.tool_call_id,
      toolName: step.name,
      args: step.args ?? {},
      result: step.status === "completed" ? { _completed: true } : undefined,
    })
  }

  // Text content
  if (stream.deltaText) {
    parts.push({ type: "text" as const, text: stream.deltaText })
  }

  // If only tool calls (no text yet), add empty text so message is renderable
  if (!stream.deltaText && !stream.thinkingText && stream.steps.length > 0) {
    parts.push({ type: "text" as const, text: "" })
  }

  return {
    id: `streaming-${stream.runId}`,
    role: "assistant",
    content: parts as ThreadMessageLike["content"],
    status: { type: "running" },
    createdAt: new Date(),
  }
}

// ---------------------------------------------------------------------------
// Hook: useParsarChatRuntime
// ---------------------------------------------------------------------------

export function useParsarChatRuntime(config: ParsarRuntimeConfig) {
  const {
    conversationId,
    timelineMessages,
    timelineRuns,
    onSendMessage,
    onCancelAll,
    invalidateTimeline,
    onStartError,
  } = config

  const [stream, setStream] = useState<StreamState>(IDLE_STREAM)
  const [isRunning, setIsRunning] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  // Expose stream state for external consumers (interaction cards, etc.)
  const pendingInteraction = stream.pendingInteraction
  const streamError = stream.error
  const activeRunId = stream.runId

  // --- Convert persisted timeline messages ---
  const convertedMessages = useMemo<ThreadMessageLike[]>(() => {
    return timelineMessages.map((msg) => convertTimelineMessage(msg, timelineRuns))
  }, [timelineMessages, timelineRuns])

  // --- Build streaming message (in-flight) ---
  const streamingMessage = useMemo(() => buildStreamingMessage(stream), [stream])

  // --- Merge: persisted + streaming ---
  const allMessages = useMemo<ThreadMessageLike[]>(() => {
    if (!streamingMessage) return convertedMessages
    return [...convertedMessages, streamingMessage]
  }, [convertedMessages, streamingMessage])

  // --- Clean up EventSource on unmount or conversation switch ---
  useEffect(() => {
    return () => {
      eventSourceRef.current?.close()
      eventSourceRef.current = null
      setStream(IDLE_STREAM)
      setIsRunning(false)
    }
  }, [conversationId])

  // --- When stream finishes (no more runId + not running), refetch timeline ---
  const prevRunIdRef = useRef<string | null>(null)
  useEffect(() => {
    if (prevRunIdRef.current && !stream.runId && !isRunning) {
      // Stream just ended — refetch timeline
      invalidateTimeline()
    }
    prevRunIdRef.current = stream.runId
  }, [stream.runId, isRunning, invalidateTimeline])

  // --- SSE subscription logic ---
  const subscribeToStream = useCallback(
    (runId: string) => {
      // Close any existing connection
      eventSourceRef.current?.close()

      const url = `/api/v1/conversations/${encodeURIComponent(conversationId)}/runs/${encodeURIComponent(runId)}/stream`
      const source = new EventSource(url)
      eventSourceRef.current = source

      setStream({
        runId,
        deltaText: "",
        thinkingText: "",
        steps: [],
        pendingInteraction: null,
        error: null,
      })
      setIsRunning(true)

      source.addEventListener("delta", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("delta", ev.data)
        if (!parsed || parsed.type !== "delta") return
        setStream((prev) => ({
          ...prev,
          deltaText: prev.deltaText + ((parsed as { delta?: string }).delta ?? ""),
          pendingInteraction: null,
        }))
      })

      source.addEventListener("thinking", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("thinking", ev.data)
        if (!parsed) return
        setStream((prev) => ({
          ...prev,
          thinkingText: prev.thinkingText + ((parsed as { thinking?: string }).thinking ?? ""),
        }))
      })

      source.addEventListener("tool", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("tool", ev.data)
        if (!parsed || parsed.type !== "tool") return
        const tool = (parsed as { tool?: StreamToolEvent }).tool
        if (!tool) return

        setStream((prev) => {
          const steps = [...prev.steps]
          if (tool.stage === "before") {
            const id = tool.id ?? `anon-${steps.length}`
            if (!steps.some((s) => s.tool_call_id === id)) {
              steps.push({
                tool_call_id: id,
                name: tool.name ?? "",
                status: "running",
                args: tool.args,
                started_at: performance.now(),
              })
            }
          } else if (tool.stage === "after") {
            const id = tool.id
            const idx = id ? steps.findIndex((s) => s.tool_call_id === id) : -1
            const endedAt = performance.now()
            if (idx >= 0) {
              steps[idx] = { ...steps[idx], status: "completed", ended_at: endedAt }
            } else {
              steps.push({
                tool_call_id: id ?? `anon-${steps.length}`,
                name: tool.name ?? "",
                status: "completed",
                args: tool.args,
                started_at: endedAt,
                ended_at: endedAt,
              })
            }
          }
          return { ...prev, steps, pendingInteraction: null }
        })
      })

      source.addEventListener("permission", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("permission", ev.data)
        if (!parsed || parsed.type !== "permission") return
        const perm = (parsed as { permission?: { id?: string; hook_decision?: unknown } }).permission
        if (!perm?.id) return
        // Plugin hook auto-decided — don't show interaction card
        if (perm.hook_decision) return
        setStream((prev) => ({
          ...prev,
          pendingInteraction: { kind: "permission", requestId: perm.id! },
        }))
      })

      source.addEventListener("prompt_for_user_choice", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("prompt_for_user_choice", ev.data)
        if (!parsed || parsed.type !== "prompt_for_user_choice") return
        const choice = (parsed as { prompt_for_user_choice?: { id?: string } }).prompt_for_user_choice
        if (!choice?.id) return
        setStream((prev) => ({
          ...prev,
          pendingInteraction: { kind: "user_choice", requestId: choice.id! },
        }))
      })

      source.addEventListener("done", (ev: MessageEvent<string>) => {
        const parsed = parseSSEData("done", ev.data)
        if (!parsed || parsed.type !== "done") return
        source.close()
        eventSourceRef.current = null
        setIsRunning(false)
        setStream((prev) => ({ ...prev, runId: null, pendingInteraction: null, error: null }))
      })

      source.addEventListener("error", (ev: Event) => {
        const data = "data" in ev && typeof (ev as MessageEvent).data === "string"
          ? (ev as MessageEvent<string>).data
          : ""
        const parsed = data ? parseSSEData("error", data) : null
        const message = parsed?.type === "error"
          ? (parsed as { error?: string }).error ?? "stream connection failed"
          : "stream connection failed"

        // Silent collapse cases
        if (isCompletedBeforeSubscribeError(message) || isUserCancelledError(message)) {
          source.close()
          eventSourceRef.current = null
          setIsRunning(false)
          setStream((prev) => ({ ...prev, runId: null, error: null, pendingInteraction: null }))
          return
        }

        // Late-error with prior content → collapse to done silently
        setStream((prev) => {
          const sawContent =
            prev.deltaText.length > 0 || prev.steps.length > 0 || prev.thinkingText.length > 0
          if (sawContent) {
            source.close()
            eventSourceRef.current = null
            setIsRunning(false)
            return { ...prev, runId: null, error: null, pendingInteraction: null }
          }
          // True error — no content received
          source.close()
          eventSourceRef.current = null
          setIsRunning(false)
          return { ...prev, runId: null, error: message, pendingInteraction: null }
        })
      })
    },
    [conversationId],
  )

  // --- onNew: send user message + subscribe to stream ---
  const onNew = useCallback(
    async (message: AppendMessage) => {
      const textPart = message.content.find((p) => p.type === "text")
      const text = textPart && "text" in textPart ? textPart.text : ""
      if (!text.trim()) return

      setIsRunning(true)

      try {
        const resp = await onSendMessage(text.trim())
        const runId = resp.agent_run_id ?? (resp.run_ids?.[0] ?? null)

        if (runId) {
          subscribeToStream(runId)
          // Fire-and-forget /start (server auto-starts, this is tolerant fallback)
          void startAgentRun(conversationId, runId).catch((err) => {
            onStartError?.(err instanceof Error ? err.message : String(err))
          })
        } else {
          // No run dispatched (unlikely) — just refetch timeline
          setIsRunning(false)
          invalidateTimeline()
        }
      } catch (err) {
        setIsRunning(false)
        onStartError?.(err instanceof Error ? err.message : String(err))
      }
    },
    [conversationId, onSendMessage, subscribeToStream, invalidateTimeline, onStartError],
  )

  // --- onCancel: cancel all + immediately stop UI ---
  const onCancel = useCallback(async () => {
    eventSourceRef.current?.close()
    eventSourceRef.current = null
    setIsRunning(false)
    setStream(IDLE_STREAM)
    onCancelAll()
  }, [onCancelAll])

  // --- Build the runtime ---
  const runtime = useExternalStoreRuntime({
    messages: allMessages,
    convertMessage: (msg: ThreadMessageLike) => msg,
    isRunning,
    onNew,
    onCancel,
  })

  return {
    runtime,
    /** Whether a stream is actively receiving events */
    isRunning,
    /** The currently active run ID (for per-run cancel) */
    activeRunId,
    /** Pending interaction from SSE (for ConversationInteractionCards) */
    pendingInteraction,
    /** Stream error (only set when no content was received) */
    streamError,
    /** Current streaming tool steps (for WorkingSteps display) */
    streamingSteps: stream.steps,
    /** Whether we have active streaming content (for polling pause) */
    hasActiveStream: !!stream.runId,
    /** Immediately stop the stream (for Stop button) */
    stopStream: () => {
      eventSourceRef.current?.close()
      eventSourceRef.current = null
      setIsRunning(false)
      setStream(IDLE_STREAM)
    },
  }
}
