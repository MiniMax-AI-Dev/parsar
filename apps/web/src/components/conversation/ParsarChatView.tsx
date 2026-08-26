/**
 * ParsarChatView — replaces the old ChatStream component.
 *
 * Integrates:
 * - Chat header (status badge + cancel all + plugin slot)
 * - AssistantRuntimeProvider with ParsarThread
 * - ConversationInteractionCards
 * - Queued run indicators
 * - Stream error banner
 * - ParsarComposer
 * - ChatErrorToast
 */

import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { Clock } from "lucide-react"

import { AssistantRuntimeProvider } from "@assistant-ui/react"

import { ParsarThread } from "./ParsarThread"
import { ConversationInteractionCards } from "./ConversationInteractionCards"
import { WorkingSteps } from "./StepDisplay"
import { Skeleton } from "../ui/skeleton"
import { ListSlot } from "../plugin/SlotRenderer"
import { cn } from "../../lib/utils"

import {
  useConversation,
  useConversationTimeline,
  useSendUserMessage,
} from "../../lib/api-conversations"
import { useCancelRun, useCancelConversation } from "../../lib/api-agents"
import { useParsarChatRuntime } from "../../lib/parsar-chat-runtime"
import type { Agent } from "../../lib/api-types"
import { useAdminView } from "../../lib/admin-router"

interface ParsarChatViewProps {
  conversationId: string
  agent: Agent | undefined
  sidebarFolded?: boolean
  sandboxGuard?: { blocked: boolean; message: string }
}

export function ParsarChatView({
  conversationId,
  agent,
  sidebarFolded,
  sandboxGuard,
}: ParsarChatViewProps) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const qc = useQueryClient()

  // --- Data queries ---
  const convInfoQ = useConversation(conversationId, null)
  const convWorkspaceId = convInfoQ.data?.workspace_id ?? null

  const sendMut = useSendUserMessage(conversationId || null)
  const cancelRunMut = useCancelRun(convWorkspaceId)
  const cancelConvMut = useCancelConversation()

  // Toast for /start errors
  const [chatToast, setChatToast] = useState<string | null>(null)

  // Polling pause state — synced from runtime's hasActiveStream below
  const [streamActive, setStreamActive] = useState(false)

  // --- Runtime ---
  const timelineQ = useConversationTimeline(conversationId, undefined, {
    pollingEnabled: !streamActive,
  })
  const messages = useMemo(() => timelineQ.data?.messages ?? [], [timelineQ.data?.messages])
  const runs = useMemo(() => timelineQ.data?.agent_runs ?? [], [timelineQ.data?.agent_runs])

  const chatRuntime = useParsarChatRuntime({
    conversationId,
    workspaceId: convWorkspaceId,
    timelineMessages: messages,
    timelineRuns: runs,
    timelineLoading: timelineQ.isLoading,
    onSendMessage: async (content: string) => {
      return sendMut.mutateAsync({ content })
    },
    onCancelAll: () => {
      cancelConvMut.mutate({
        conversationID: conversationId,
        reason: "user_clicked_cancel_all",
      })
    },
    onCancelRun: (runId: string) => {
      cancelRunMut.mutate({ runID: runId, reason: "user_clicked_cancel" })
    },
    invalidateTimeline: () => {
      qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", conversationId] })
    },
    onStartError: setChatToast,
  })

  const {
    runtime,
    isRunning,
    activeRunId,
    pendingInteraction,
    streamError,
    streamingSteps,
    hasActiveStream,
    stopStream,
  } = chatRuntime

  // Keep polling paused while streaming
  // eslint-disable-next-line react-hooks/set-state-in-effect -- sync external streaming state to polling control
  useEffect(() => { setStreamActive(hasActiveStream) }, [hasActiveStream])

  // Queued runs for indicators
  const queuedRuns = useMemo(
    () => runs.filter((r) => r.status === "queued"),
    [runs],
  )

  // --- Stop handler: immediately stop + cancel the run ---
  const handleStop = () => {
    if (activeRunId) {
      stopStream()
      cancelRunMut.mutate({ runID: activeRunId, reason: "user_clicked_stop" })
    }
  }

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {/* Minimal header — agent name + plugin slot only */}
      <div
        className={cn(
          "border-b border-line/40 px-5 py-2 sm:px-6 lg:px-10",
          sidebarFolded && "pl-14 sm:pl-16 lg:pl-[72px]",
        )}
      >
        <div className="mx-auto flex max-w-[var(--thread-max-width,48rem)] items-center justify-between gap-4">
          <h2 className="truncate text-sm font-medium text-fg-muted">
            {agent?.name || t("conversations.sidebar.allAgentsHint")}
          </h2>
          <div className="flex shrink-0 items-center gap-2">
            <ListSlot
              slotId="conversation.header.actions"
              context={{ conversationId, agent }}
            />
          </div>
        </div>
      </div>

      {/* Thread — messages + composer with auto-scroll */}
      {timelineQ.isLoading ? (
        <div className="flex-1 space-y-4 p-8">
          <Skeleton className="h-20 w-2/3" />
          <Skeleton className="ml-auto h-20 w-3/4" />
          <Skeleton className="h-32 w-2/3" />
        </div>
      ) : (
        <ParsarThread
          onStop={activeRunId ? handleStop : undefined}
          cancelling={cancelRunMut.isPending}
          disabled={!agent || sandboxGuard?.blocked}
          agentName={agent?.name}
          conversationId={conversationId}
        />
      )}

      {/* Below thread: interaction cards + queued indicators + working steps + error */}
      <div className="mx-auto w-full max-w-4xl px-5 sm:px-6 lg:px-10">
        {/* Interaction cards (permission / user choice) */}
        <ConversationInteractionCards
          workspaceID={convWorkspaceId}
          conversationID={conversationId}
          preferredRequestID={pendingInteraction?.requestId}
          onOpenInbox={() => navigate("approvals")}
        />

        {/* Working steps (streaming tool calls without delta text) */}
        {isRunning && !hasActiveStream && streamingSteps.length === 0 && (
          <div className="mt-3 flex w-fit items-center gap-2 rounded-md bg-surface px-3 py-2 text-sm text-fg-subtle shadow-sm ring-1 ring-line/70">
            <span className="flex items-center gap-1" aria-hidden="true">
              <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success [animation-delay:-300ms]" />
              <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success [animation-delay:-150ms]" />
              <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success" />
            </span>
            {t("conversations.stream.thinking")}
          </div>
        )}

        {isRunning && streamingSteps.length > 0 && (
          <div className="mt-3">
            <WorkingSteps
              steps={streamingSteps}
              active={isRunning}
              cancelling={cancelRunMut.isPending}
              onCancel={activeRunId ? handleStop : undefined}
            />
          </div>
        )}

        {/* Queued run chips */}
        {queuedRuns.map((r) => (
          <div
            key={r.id}
            className="mt-3 flex w-fit items-center gap-2 rounded-md border border-line/70 bg-surface-subtle px-3 py-2 text-sm text-fg-subtle shadow-sm"
          >
            <Clock className="h-3.5 w-3.5" strokeWidth={2.25} aria-hidden="true" />
            {r.queue_position && r.queue_position > 1
              ? t("conversations.stream.queuedWithPosition", { position: r.queue_position })
              : t("conversations.stream.queued")}
          </div>
        ))}

        {/* Stream error banner */}
        {streamError && (
          <div className="mt-3 rounded-lg border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
            {t("conversations.stream.error", { error: streamError })}
          </div>
        )}
      </div>

      {/* Chat toast (for /start failures) */}
      {chatToast && (
        <div className="mx-auto w-full max-w-4xl px-5 sm:px-6 lg:px-10">
          <div className="mb-2 flex items-start justify-between gap-3 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
            <span className="break-words">{chatToast}</span>
            <button
              type="button"
              onClick={() => setChatToast(null)}
              className="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium text-danger-emphasis hover:bg-danger-subtle"
            >
              ×
            </button>
          </div>
        </div>
      )}

    </AssistantRuntimeProvider>
  )
}
