/**
 * ParsarChatView — the assistant-ui conversation surface.
 *
 * Integrates:
 * - A 48px hairline header (agent name + plugin slot)
 * - AssistantRuntimeProvider with ParsarThread
 * - ConversationInteractionCards
 * - Queued run lines, working steps, stream error line
 * - ChatErrorToast (inline, dismissible)
 */

import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, X } from "lucide-react"

import { AssistantRuntimeProvider } from "@assistant-ui/react"

import { ParsarThread } from "./ParsarThread"
import { ConversationInteractionCards } from "./ConversationInteractionCards"
import { WorkingSteps } from "./StepDisplay"
import { Button } from "../ui/button"
import { Skeleton } from "../ui/skeleton"
import { StatusIcon } from "../ui/status-icon"
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
  const { t: tc } = useTranslation("common")
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
      {/* 64px header (matches the shell topbar) — agent name + plugin slot */}
      <div
        className={cn(
          "flex h-16 shrink-0 items-center gap-2 border-b border-line px-4",
          sidebarFolded && "pl-12",
        )}
      >
        <h2 className="m-0 min-w-0 flex-1 truncate text-sm font-medium text-fg">
          {agent?.name || t("conversations.sidebar.allAgentsHint")}
        </h2>
        <div className="flex shrink-0 items-center gap-2">
          <ListSlot
            slotId="conversation.header.actions"
            context={{ conversationId, agent }}
          />
        </div>
      </div>

      {/* Thread — messages + composer with auto-scroll */}
      {timelineQ.isLoading ? (
        <div className="mx-auto w-full max-w-[var(--thread-max-width,48rem)] flex-1 space-y-4 px-4 py-6">
          <Skeleton className="ml-auto h-9 w-2/3" />
          <Skeleton className="h-16 w-3/4" />
          <Skeleton className="ml-auto h-9 w-1/2" />
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

      {/* Below thread: interaction cards + queued lines + working steps + error */}
      <div className="mx-auto w-full max-w-[var(--thread-max-width,48rem)] space-y-3 px-4 empty:hidden">
        {/* Interaction cards (permission / user choice) */}
        <ConversationInteractionCards
          workspaceID={convWorkspaceId}
          conversationID={conversationId}
          preferredRequestID={pendingInteraction?.requestId}
        />

        {/* Thinking line (running, no streamed content or steps yet) */}
        {isRunning && !hasActiveStream && streamingSteps.length === 0 && (
          <p className="m-0 flex items-center gap-2 text-sm text-fg" role="status" aria-live="polite">
            <StatusIcon status="running" />
            {t("conversations.stream.thinking")}
          </p>
        )}

        {isRunning && streamingSteps.length > 0 && (
          <WorkingSteps
            steps={streamingSteps}
            active={isRunning}
            cancelling={cancelRunMut.isPending}
            onCancel={activeRunId ? handleStop : undefined}
          />
        )}

        {/* Queued run lines */}
        {queuedRuns.map((r) => (
          <p key={r.id} className="m-0 flex items-center gap-2 text-sm text-fg">
            <StatusIcon status="queued" />
            {r.queue_position && r.queue_position > 1
              ? t("conversations.stream.queuedWithPosition", { position: r.queue_position })
              : t("conversations.stream.queued")}
          </p>
        ))}

        {/* Stream error line */}
        {streamError && (
          <p className="m-0 flex items-start gap-1.5 text-sm text-fg">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            <span>{t("conversations.stream.error", { error: streamError })}</span>
          </p>
        )}

        {/* Chat toast (for /start failures) */}
        {chatToast && (
          <div className="flex items-start gap-1.5 text-sm text-fg">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            <span className="min-w-0 flex-1 break-words">{chatToast}</span>
            <Button
              variant="ghost"
              size="icon"
              className="-my-1.5 h-6 w-6"
              onClick={() => setChatToast(null)}
              aria-label={tc("actions.close")}
            >
              <X strokeWidth={1.5} aria-hidden="true" />
            </Button>
          </div>
        )}
      </div>
    </AssistantRuntimeProvider>
  )
}
