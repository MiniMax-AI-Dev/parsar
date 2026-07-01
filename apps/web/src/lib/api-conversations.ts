import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type {
  AgentRunStreamEvent,
  Conversation,
  CreateConversationRequest,
  ConversationTimeline,
  ListConversationsResponse,
  SendUserMessageRequest,
  SendUserMessageResponse,
  StartAgentRunResponse,
} from "./api-types"

/* --- Query keys --------------------------------------------------------- */

const KEY_LIST = (wsId: string, agentID: string) =>
  ["admin", "conversations", wsId, agentID || "_all"] as const
const KEY_ONE = (cid: string) => ["admin", "conversation", cid] as const
const KEY_TIMELINE = (cid: string) => ["admin", "conversationTimeline", cid] as const

/* --- Network ------------------------------------------------------------ */

async function listConversations(
  wsId: string | null,
  agentID: string
): Promise<ListConversationsResponse> {
  if (!wsId) return { conversations: [] }
  const query: Record<string, string | number> = { limit: 200 }
  if (agentID) query.agent_id = agentID
  return apiRequest<ListConversationsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/conversations`,
    { query }
  )
}

async function getConversation(cid: string): Promise<Conversation> {
  return apiRequest<Conversation>(`/api/v1/conversations/${encodeURIComponent(cid)}`)
}

async function getTimeline(cid: string): Promise<ConversationTimeline> {
  return apiRequest<ConversationTimeline>(
    `/api/v1/conversations/${encodeURIComponent(cid)}/timeline`,
    { query: { limit: 100 } }
  )
}

export async function createConversation(
  wsId: string,
  body: CreateConversationRequest
): Promise<Conversation> {
  return apiRequest<Conversation>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/conversations`,
    { method: "POST", body }
  )
}

export async function sendUserMessage(
  cid: string,
  body: SendUserMessageRequest
): Promise<SendUserMessageResponse> {
  return apiRequest<SendUserMessageResponse>(
    `/api/v1/conversations/${encodeURIComponent(cid)}/messages`,
    { method: "POST", body }
  )
}

export async function startAgentRun(cid: string, runId: string): Promise<StartAgentRunResponse> {
  return apiRequest<StartAgentRunResponse>(
    `/api/v1/conversations/${encodeURIComponent(cid)}/runs/${encodeURIComponent(runId)}/start`,
    { method: "POST" }
  )
}

export async function updateConversationTitle(cid: string, title: string): Promise<Conversation> {
  return apiRequest<Conversation>(
    `/api/v1/conversations/${encodeURIComponent(cid)}`,
    { method: "PATCH", body: { title } }
  )
}

export async function deleteConversation(cid: string): Promise<void> {
  await apiRequest<void>(
    `/api/v1/conversations/${encodeURIComponent(cid)}`,
    { method: "DELETE" }
  )
}

export interface StreamingStep {
  tool_call_id: string
  name: string
  status: "running" | "completed"
  // Tool input arguments captured on `before` so the live card can show a
  // one-line command summary (e.g. `BASH find / -maxdepth 4 ...`). Optional
  // because some connectors don't surface args.
  args?: Record<string, unknown>
  // Wall-clock timestamps (ms since page load) for live elapsed display.
  started_at: number
  ended_at?: number
}

export interface AgentRunStreamState {
  status: "idle" | "streaming" | "done" | "error"
  deltaText: string
  steps: StreamingStep[]
  final: string | null
  error: string | null
}

const idleStreamState: AgentRunStreamState = {
  status: "idle",
  deltaText: "",
  steps: [],
  final: null,
  error: null,
}

function parseStreamEvent(type: AgentRunStreamEvent["type"], raw: string): AgentRunStreamEvent | null {
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

// Matches the synthesized hang-error reason written by server run_stream.go
// writeStreamHangError when the run is already cancelled by the time the 30s
// first-event timer fires. Treated as clean termination (the user clicked
// 取消全部 / /cancel), not an error. Keep prefix in sync with run_stream.go.
function isUserCancelledError(message: string): boolean {
  return message.startsWith("run_cancelled:")
}

/* --- React Query hooks -------------------------------------------------- */

export function useConversations(wsId: string | null, agentID: string = "") {
  return useQuery({
    queryKey: KEY_LIST(wsId ?? "_none", agentID),
    queryFn: () => listConversations(wsId, agentID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
  })
}

export function useConversation(cid: string | null, _workspaceIDForMock?: string | null) {
  return useQuery({
    queryKey: KEY_ONE(cid ?? "_none"),
    queryFn: () => {
      if (!cid) throw new Error("conversation id is required")
      return getConversation(cid)
    },
    enabled: !!cid,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })
}

export function useConversationTimeline(
  cid: string | null,
  _workspaceIDForMock?: string | null,
  opts: { pollingEnabled?: boolean } = {}
) {
  const pollingEnabled = opts.pollingEnabled ?? true
  return useQuery({
    queryKey: KEY_TIMELINE(cid ?? "_none"),
    queryFn: () => {
      if (!cid) throw new Error("conversation id is required")
      return getTimeline(cid)
    },
    enabled: !!cid,
    retry: noUnreachableRetry,
    staleTime: 15_000,
    refetchInterval: () => (pollingEnabled && document.visibilityState === "visible" ? 5_000 : false),
  })
}

export function useAgentRunStream(
  cid: string | null,
  runId: string | null,
  opts: { enabled?: boolean } = {}
): AgentRunStreamState {
  const enabled = opts.enabled ?? true
  const [state, setState] = useState<AgentRunStreamState>(idleStreamState)

  useEffect(() => {
    if (!cid || !runId || !enabled) {
      setState(idleStreamState)
      return
    }

    const source = new EventSource(
      `/api/v1/conversations/${encodeURIComponent(cid)}/runs/${encodeURIComponent(runId)}/stream`
    )
    setState({ status: "streaming", deltaText: "", steps: [], final: null, error: null })

    const onDelta = (ev: MessageEvent<string>) => {
      const parsed = parseStreamEvent("delta", ev.data)
      if (!parsed || parsed.type !== "delta") return
      setState((prev) => ({
        ...prev,
        status: prev.status === "done" ? prev.status : "streaming",
        deltaText: prev.deltaText + (parsed.delta ?? ""),
      }))
    }

    const onTool = (ev: MessageEvent<string>) => {
      const parsed = parseStreamEvent("tool", ev.data)
      if (!parsed || parsed.type !== "tool" || !parsed.tool) return
      const { id, name, stage, args } = parsed.tool
      setState((prev) => {
        const steps = [...prev.steps]
        if (stage === "before") {
          if (!id || !steps.some((s) => s.tool_call_id === id)) {
            steps.push({
              tool_call_id: id ?? `anon-${steps.length}`,
              name: name ?? "",
              status: "running",
              args,
              started_at: performance.now(),
            })
          }
        } else if (stage === "after") {
          const idx = id ? steps.findIndex((s) => s.tool_call_id === id) : -1
          const endedAt = performance.now()
          if (idx >= 0) {
            steps[idx] = { ...steps[idx], status: "completed", ended_at: endedAt }
          } else {
            steps.push({
              tool_call_id: id ?? `anon-${steps.length}`,
              name: name ?? "",
              status: "completed",
              args,
              // No `before` was seen — fall back to endedAt so the elapsed
              // pill renders as ~0ms rather than 'NaNs'.
              started_at: endedAt,
              ended_at: endedAt,
            })
          }
        }
        return { ...prev, steps }
      })
    }

    const onDone = (ev: MessageEvent<string>) => {
      const parsed = parseStreamEvent("done", ev.data)
      if (!parsed || parsed.type !== "done") return
      setState((prev) => ({
        ...prev,
        status: "done",
        final: parsed.final?.content ?? "",
        error: null,
      }))
      source.close()
    }

    const onError = (ev: Event) => {
      const data = "data" in ev && typeof ev.data === "string" ? ev.data : ""
      const parsed = data ? parseStreamEvent("error", data) : null
      const message = parsed?.type === "error" ? parsed.error : "stream connection failed"
      if (isCompletedBeforeSubscribeError(message)) {
        setState((prev) => ({ ...prev, status: "done", final: prev.final, error: null }))
        source.close()
        return
      }
      // User clicked 取消全部 / sent /cancel — the server's 30s hang timer
      // fires because the dispatcher never published an event before the
      // abort took effect. Collapse to status='done' so the red 流式输出中断
      // banner stays hidden.
      if (isUserCancelledError(message)) {
        setState((prev) => ({ ...prev, status: "done", final: prev.final, error: null }))
        source.close()
        return
      }
      // Late-error race: this connection already received real content
      // (delta tokens, tool steps, or a Done frame) but the server's hang
      // timer / a downstream reconnect synthesizes an error. Refuse to
      // surface the red banner — collapse to done so the timeline refetch
      // picks up the persisted assistant message.
      setState((prev) => {
        const sawContent =
          prev.deltaText.length > 0 ||
          prev.steps.length > 0 ||
          prev.final !== null
        if (sawContent) {
          return { ...prev, status: "done", error: null }
        }
        return { ...prev, status: "error", error: message || "stream connection failed" }
      })
      source.close()
    }

    source.addEventListener("delta", onDelta)
    source.addEventListener("tool", onTool)
    source.addEventListener("done", onDone)
    source.addEventListener("error", onError)

    return () => {
      source.close()
    }
  }, [cid, enabled, runId])

  return state
}

export function useCreateConversation(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateConversationRequest) => {
      if (!wsId) throw new Error("workspace id is required")
      return createConversation(wsId, body)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      // New conversation may be bound to any agent, so invalidate every
      // per-agent slice for this workspace. Predicate matches both
      // ['admin','conversations',wsId,'_all'] and the per-agent variant.
      if (wsId) {
        qc.invalidateQueries({
          predicate: (q) =>
            q.queryKey[0] === "admin" &&
            q.queryKey[1] === "conversations" &&
            q.queryKey[2] === wsId,
        })
      }
    },
  })
}

export function useSendUserMessage(cid: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: SendUserMessageRequest) => {
      if (!cid) throw new Error("conversation id is required")
      return sendUserMessage(cid, body)
    },
    retry: noUnreachableRetry,
    onSuccess: () => {
      if (!cid) return
      qc.invalidateQueries({ queryKey: KEY_ONE(cid) })
      qc.invalidateQueries({ queryKey: KEY_TIMELINE(cid) })
      qc.invalidateQueries({
        predicate: (q) => q.queryKey[0] === "admin" && q.queryKey[1] === "conversations",
      })
    },
  })
}


/* --- Rename / delete ---------------------------------------------------- */

/**
 * Update a conversation's user-visible title. Server trims and caps at 200
 * chars; 422 on empty / too-long, 404 on unknown / already-deleted.
 */
export function useUpdateConversationTitle(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (args: { cid: string; title: string }) =>
      updateConversationTitle(args.cid, args.title),
    retry: noUnreachableRetry,
    onSuccess: (_data, args) => {
      qc.invalidateQueries({ queryKey: KEY_ONE(args.cid) })
      if (wsId) {
        qc.invalidateQueries({
          predicate: (q) =>
            q.queryKey[0] === "admin" &&
            q.queryKey[1] === "conversations" &&
            q.queryKey[2] === wsId,
        })
      }
    },
  })
}

/**
 * Soft-delete a conversation. 204 on success, 404 if already deleted
 * (idempotent). Caller is expected to navigate away before next render —
 * passing the deleted id back to useConversation() returns 404 and triggers
 * the EmptyChat fallback in ConversationsPage.
 */
export function useDeleteConversation(wsId: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (cid: string) => deleteConversation(cid),
    retry: noUnreachableRetry,
    onSuccess: (_data, cid) => {
      qc.invalidateQueries({ queryKey: KEY_ONE(cid) })
      qc.invalidateQueries({ queryKey: KEY_TIMELINE(cid) })
      if (wsId) {
        qc.invalidateQueries({
          predicate: (q) =>
            q.queryKey[0] === "admin" &&
            q.queryKey[1] === "conversations" &&
            q.queryKey[2] === wsId,
        })
      }
    },
  })
}
