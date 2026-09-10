/* eslint-disable react-refresh/only-export-components */
import { useQueryClient } from "@tanstack/react-query"
import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  ArrowDown,
  ArrowUp,
  ArrowUpRight,
  Loader2,
  MessageSquare,
  PanelLeftOpen,
  Square,
  X,
} from "lucide-react"

import { PageHeader } from "../layout/PageHeader"
import { ApprovalBar } from "./ApprovalBar"
import { ConversationInteractionCards } from "./ConversationInteractionCards"
import { MessageMarkdown } from "./MessageMarkdown"
import { WorkTrace, type TraceStep } from "./WorkTrace"
import { RunFailureNotice } from "./RunFailureNotice"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { ErrorDialog } from "../ui/error-dialog"
import { ErrorState, InlineError } from "../ui/error-state"
import { InitialTile } from "../ui/ledger"
import { Skeleton } from "../ui/skeleton"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import { TurnNavRail, turnPreview, useActiveTurnKey } from "../ui/turn-nav-rail"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useCancelRun } from "../../lib/api-agents"
import {
  startAgentRun,
  useAgentRunStream,
  useConversation,
  useConversationTimeline,
  useSendUserMessage,
  type StreamingStep,
} from "../../lib/api-conversations"
import { useAgentInteractions } from "../../lib/api-interactions"
import type {
  AgentInteraction,
  ConversationTimelineRun,
  Agent,
  ToolStep,
} from "../../lib/api-types"
import { isUserMessageSender } from "../../lib/message-sender"
import { isRuntimeCapabilityError } from "../../lib/message-kind"
import { conversationRecoveryLinks } from "../../lib/conversation-recovery-links"
import { isFailedToolResult } from "../../lib/tool-result"
import { useRelativeTime } from "../../lib/relative-time"
import { useThreadScroll } from "../../lib/use-thread-scroll"
import { credentialKindLabel } from "../../pages/admin/capability-ui"
import { ToolCardSlot, SingleSlot, ListSlot } from "../plugin/SlotRenderer"


/** Reading measure of the thread column; the components below read it. */
const THREAD_STYLE = { ["--thread-max-width" as string]: "48rem" }

/** title · conversation id · age (actions replace the age on hover) */

import type { SandboxSendGuard } from "../../lib/sandbox-send-guard"

/* ============================================================== */
/*  The conversation thread, mounted by the console and by /c/<id> */
/* ============================================================== */

/**
 * The conversation thread: header, stream, work trace, approval bar and
 * composer. It is a surface, not a page — the console mounts it beside its
 * list, and a shared link mounts it alone. One implementation, two shells.
 */
interface MainProps {
  conv: import("../../lib/api-types").Conversation | undefined
  canWrite: boolean
  convLoading: boolean
  convError: unknown
  agent: Agent | undefined
  conversationId: string
  /** From the list summary; single-conv GET doesn't include it. */
  messageCount: number
  folded: boolean
  onExpand: () => void
  /** Empty-state send: create conv + post first message + navigate. */
  onSendFromEmpty: (content: string) => Promise<boolean>
  onRenameAfterFirstMessage: (cid: string, title: string) => Promise<void>
  focusComposer?: boolean
  sandboxGuard?: SandboxSendGuard
  /**
   * "console" draws the admin topbar above the thread; "bare" leaves it out
   * for a shell that already names what you are looking at.
   */
  chrome?: "console" | "bare"
}

export function ConversationMain(p: MainProps) {
  const err = p.convError
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  // workspace.content slot: plugin can replace the conversation content
  // area while keeping the navigation list intact.
  return (
    <SingleSlot
      slotId="workspace.content"
      context={{ agent: p.agent, conversationId: p.conversationId }}
      fallback={
        <ConversationMainInner err={err} isUnreachable={isUnreachable ?? false} {...p} />
      }
    />
  )
}

/** The page's 48px topbar; the fold toggle sits before the title when the list is hidden. */
function ThreadHeader({
  folded,
  onExpand,
  actions,
}: {
  folded: boolean
  onExpand: () => void
  actions?: React.ReactNode
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const pageTitle = tc("nav.items.conversations")
  return (
    <PageHeader
      className="static mx-0 mb-0"
      title={pageTitle}
      subtitleFor="conversations.page.title"
      backLink={
        folded ? (
          <Button
            variant="ghost"
            size="icon"
            onClick={onExpand}
            aria-label={t("conversations.sidebar.expandAria")}
            title={t("conversations.sidebar.expandAria")}
          >
            <PanelLeftOpen strokeWidth={1.5} aria-hidden="true" />
          </Button>
        ) : undefined
      }
      action={actions}
    />
  )
}

function ConversationMainInner(p: MainProps & { err: unknown; isUnreachable: boolean }) {
  const { t } = useTranslation("admin")
  const { err, isUnreachable } = p

  if (err) {
    return (
      <div className="flex min-w-0 flex-1 flex-col">
        {p.chrome !== "bare" && <ThreadHeader folded={p.folded} onExpand={p.onExpand} />}
        <div className="px-6 pt-6">
          <ErrorState
            title={
              isUnreachable
                ? t("conversations.loadError.unreachable.title")
                : t("conversations.loadError.title")
            }
            description={isUnreachable ? t("conversations.loadError.unreachable.description") : t("conversations.loadError.description")}
            detail={!isUnreachable && err instanceof Error ? err.message : undefined}
            hint={
              isUnreachable
                ? t("conversations.loadError.unreachable.hint")
                : t("conversations.loadError.hint")
            }
          />
        </div>
      </div>
    )
  }

  if (p.convLoading) {
    return (
      <div className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
        {p.chrome !== "bare" && <ThreadHeader folded={p.folded} onExpand={p.onExpand} />}
        <div className="mx-auto w-full max-w-[var(--thread-max-width)] space-y-4 px-4 py-6">
          <Skeleton className="ml-auto h-9 w-2/3" />
          <Skeleton className="h-16 w-3/4" />
          <Skeleton className="ml-auto h-9 w-1/2" />
        </div>
      </div>
    )
  }

  if (!p.conversationId || !p.conv) {
    return (
      <EmptyChat
        agent={p.agent}
        canWrite={p.canWrite}
        folded={p.folded}
        chrome={p.chrome}
        onExpand={p.onExpand}
        onSendFromEmpty={p.onSendFromEmpty}
        focusComposer={p.focusComposer}
        sandboxGuard={p.sandboxGuard}
      />
    )
  }

  if (p.messageCount === 0 && !p.conv.primary_agent_deleted) {
    // 0 messages: same empty surface as the no-conversation state until
    // the first send; interaction cards still show for this conv.
    return (
      <EmptyChat
        agent={p.agent}
        canWrite={p.canWrite}
        folded={p.folded}
        chrome={p.chrome}
        onExpand={p.onExpand}
        conversationId={p.conversationId}
        workspaceID={p.conv.workspace_id}
        onRenameAfterFirstMessage={p.onRenameAfterFirstMessage}
        focusComposer={p.focusComposer}
        sandboxGuard={p.sandboxGuard}
      />
    )
  }

  return (
    <ChatStream
      conversationId={p.conversationId}
      canWrite={p.canWrite}
      agent={p.agent}
      folded={p.folded}
      chrome={p.chrome}
      onExpand={p.onExpand}
      sandboxGuard={p.sandboxGuard}
    />
  )
}

/** Hairline-topped footer that holds the composer at the thread's measure. */
function ComposerFooter({ children }: { children: React.ReactNode }) {
  return (
    <div className="shrink-0 px-4 pb-5 pt-2">
      <div className="mx-auto w-full max-w-[var(--thread-max-width)] px-4">{children}</div>
    </div>
  )
}

/* ============================================================== */
/*  Empty state — greeting + composer                              */
/* ============================================================== */

function EmptyChat({
  agent,
  folded,
  onExpand,
  chrome,
  conversationId,
  workspaceID,
  onSendFromEmpty,
  onRenameAfterFirstMessage,
  focusComposer,
  sandboxGuard,
  canWrite,
}: {
  agent: Agent | undefined
  folded: boolean
  onExpand: () => void
  chrome?: "console" | "bare"
  /** When set, composer sends into this conv (in-chat flow). */
  conversationId?: string
  workspaceID?: string
  /** Create-then-send flow (required when conversationId is unset). */
  onSendFromEmpty?: (content: string) => Promise<boolean>
  onRenameAfterFirstMessage?: (cid: string, title: string) => Promise<void>
  focusComposer?: boolean
  sandboxGuard?: SandboxSendGuard
  canWrite: boolean
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
      {chrome !== "bare" && <ThreadHeader
        folded={folded}
        onExpand={onExpand}
        actions={
          conversationId ? (
            <ListSlot slotId="conversation.header.actions" context={{ conversationId, agent }} />
          ) : undefined
        }
      />}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[var(--thread-max-width)] flex-1 flex-col px-4">
          {conversationId && workspaceID ? (
            <div className="pt-6">
              <ConversationInteractionCards workspaceID={workspaceID} conversationID={conversationId} />
            </div>
          ) : null}
          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              icon={MessageSquare}
              title={t("conversations.empty.greet")}
              description={agent ? undefined : t("conversations.empty.placeholderNoAgent")}
            />
          </div>
        </div>
      </div>
      <ComposerFooter>
        <ComposerForm
          conversationId={conversationId ?? ""}
          agentName={agent?.name}
          disabled={!canWrite || !agent || sandboxGuard?.blocked}
          autoFocus={focusComposer}
          placeholder={
            agent
              ? t("conversations.empty.placeholderWithAgent", { agent: agent.name })
              : t("conversations.empty.placeholderNoAgent")
          }
          onSendDirect={!conversationId && agent ? onSendFromEmpty : undefined}
          onAfterSend={
            conversationId && onRenameAfterFirstMessage
              ? (title) => onRenameAfterFirstMessage(conversationId, title)
              : undefined
          }
          blockReason={sandboxGuard?.blocked ? sandboxGuard.message : undefined}
        />
      </ComposerFooter>
    </div>
  )
}

/* ============================================================== */
/*  Chat stream — user (muted block, right) + agent (ink, left)     */
/* ============================================================== */

function ChatStream({
  conversationId,
  canWrite,
  agent,
  folded,
  onExpand,
  chrome,
  sandboxGuard,
}: {
  conversationId: string
  canWrite: boolean
  agent: Agent | undefined
  folded: boolean
  onExpand: () => void
  chrome?: "console" | "bare"
  sandboxGuard?: SandboxSendGuard
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const { navigate } = useAdminView()
  const qc = useQueryClient()
  // `navigate` only rewrites the query string, so on the bare shell it would
  // leave the reader on /c/<id> with a junk history entry. A colleague reading
  // a shared thread has no use for the run detail either way.
  const openRun = useCallback(
    (runID: string) => { if (chrome !== "bare") navigate("runs", { id: runID }) },
    [navigate, chrome],
  )

  // /cancel infra: per-run cancel (X on the working steps) + bulk
  // cancel (header button when at least one queued/running run exists).
  // Workspace id is read from useConversation so the hook is workspace-aware
  // without threading the id through every parent prop bag.
  const convInfoQ = useConversation(conversationId, null)
  const convWorkspaceId = convInfoQ.data?.workspace_id ?? null
  const agentDeleted = convInfoQ.data?.primary_agent_deleted === true
  const agentName = agent?.name || convInfoQ.data?.primary_agent_name || ""
  const cancelRunMut = useCancelRun(convWorkspaceId)
  const cancelScopeRef = useRef<HTMLDivElement>(null)

  // SSE state: ComposerForm hands us a run_id after send; we open the
  // EventSource and append delta tokens into the streaming message. While
  // a stream is active we pause timeline polling so the half-written
  // assistant message doesn't get clobbered by a stale GET.
  const [activeRunId, setActiveRunId] = useState<string | null>(null)
  // Wall clock of the moment the composer handed us a run id: the trace's
  // clock until the timeline carries the run's own started_at.
  const [liveStartedAt, setLiveStartedAt] = useState<number | null>(null)
  const startRun = useCallback((runId: string) => {
    setChatToast(null)
    setLiveStartedAt(Date.now())
    setActiveRunId(runId)
  }, [])
  // Surface fire-and-forget /start failures (e.g. daemon offline,
  // network error). Server now auto-starts agent_daemon runs, so a
  // /start that returns 200 `already running` is fine; only true
  // network/5xx errors land here.
  const [chatToast, setChatToast] = useState<{ text: string; detail?: string; runID?: string } | null>(null)
  const stream = useAgentRunStream(conversationId, activeRunId, { enabled: !!activeRunId })
  const hasActiveStream = !!activeRunId && stream.status !== "error" && stream.status !== "done"
  // Our sentence and the server's string, kept apart. They used to be one
  // interpolated line, which put the machine's words in our mouth.
  // Memoised because it is an object now and it feeds an effect's deps: a
  // fresh literal every render would re-run that effect every render.
  const streamErrorMessage = useMemo(
    () =>
      stream.error
        ? isContextRejectedStreamError(stream.error)
          ? { text: t("conversations.stream.contextRejected") }
          : { text: t("conversations.stream.error"), detail: stream.error }
        : null,
    [stream.error, t],
  )

  const timelineQ = useConversationTimeline(conversationId, undefined, {
    pollingEnabled: !hasActiveStream,
  })
  const messages = useMemo(() => timelineQ.data?.messages ?? [], [timelineQ.data?.messages])
  const runs = useMemo(() => timelineQ.data?.agent_runs ?? [], [timelineQ.data?.agent_runs])

  // Recover the live SSE subscription after a page refresh or when opening a
  // conversation that already has a running task. The stream endpoint replays
  // persisted events before following new ones, so steps and partial output
  // catch up instead of falling back indefinitely to "Agent is replying…".
  useEffect(() => {
    if (activeRunId || cancelRunMut.isPending || timelineQ.isError || timelineQ.isStale) return
    const runningRun = runs.find((r) => r.status === "running")
    if (!runningRun) return
    const timer = window.setTimeout(() => setActiveRunId(runningRun.id), 0)
    return () => window.clearTimeout(timer)
  }, [activeRunId, cancelRunMut.isPending, timelineQ.isError, timelineQ.isStale, runs])

  // Map output_message_id → runs[] so MessageRow can read the presentation
  // and the failed-run link from the run that produced the answer.
  const runsByOutputMessage = useMemo(() => {
    const m = new Map<string, ConversationTimelineRun[]>()
    for (const r of runs) {
      if (!r.output_message_id) continue
      const arr = m.get(r.output_message_id)
      if (arr) arr.push(r)
      else m.set(r.output_message_id, [r])
    }
    return m
  }, [runs])

  // Traces anchor on the user turn that triggered the run; a run the server
  // only tied to its answer sits directly before that answer instead.
  const runsByTrigger = useMemo(() => {
    const m = new Map<string, ConversationTimelineRun[]>()
    for (const r of runs) {
      if (!r.trigger_message_id) continue
      const arr = m.get(r.trigger_message_id)
      if (arr) arr.push(r)
      else m.set(r.trigger_message_id, [r])
    }
    return m
  }, [runs])
  const orphanRunsByOutput = useMemo(() => {
    const m = new Map<string, ConversationTimelineRun[]>()
    for (const r of runs) {
      if (r.trigger_message_id || !r.output_message_id) continue
      const arr = m.get(r.output_message_id)
      if (arr) arr.push(r)
      else m.set(r.output_message_id, [r])
    }
    return m
  }, [runs])
  const visibleMessageIDs = new Set(messages.map((message) => message.id))
  const hasVisibleAnchor = (run: ConversationTimelineRun) => visibleMessageIDs.has(run.trigger_message_id || run.output_message_id || "")
  const unanchoredFailures = runs.filter((run) =>
    (run.status === "failed" || run.status === "interrupted") &&
    !hasVisibleAnchor(run),
  )
  const liveRunAnchored = !!activeRunId && runs.some((r) => r.id === activeRunId && hasVisibleAnchor(r))

  // Pending permission requests of this conversation, oldest first: they
  // take the composer's slot as the approval bar and open the run's trace.
  const interactionsQ = useAgentInteractions(convWorkspaceId, "pending")
  const pendingPermissions = useMemo<AgentInteraction[]>(
    () =>
      (interactionsQ.data?.interactions ?? [])
        .filter(
          (i) => i.conversation_id === conversationId && i.kind === "permission" && i.status === "pending",
        )
        .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at)),
    [interactionsQ.data?.interactions, conversationId],
  )
  const runsAwaitingUser = useMemo(
    () => new Set(pendingPermissions.map((i) => i.agent_run_id)),
    [pendingPermissions],
  )
  // The stream announces the request before the 2s poll would: refetch now.
  const pendingRequestId = stream.pendingInteraction?.requestId
  useEffect(() => {
    if (!pendingRequestId || !convWorkspaceId) return
    qc.invalidateQueries({ queryKey: ["admin", "interactions", convWorkspaceId, "pending"] })
  }, [pendingRequestId, convWorkspaceId, qc])

  // Turn navigation: one marker per user turn, the active one follows scroll.
  const [viewport, setViewport] = useState<HTMLDivElement | null>(null)
  const [threadContent, setThreadContent] = useState<HTMLDivElement | null>(null)
  const { scrollToLatest, scrollToTurn, showScrollToLatest } = useThreadScroll(conversationId, viewport, threadContent)
  const turns = useMemo(
    () =>
      messages
        .filter((m) => isUserMessageSender(m.sender_type))
        .map((m) => ({ key: m.id, preview: turnPreview(m.content) })),
    [messages],
  )
  const turnKeys = useMemo(() => turns.map((turn) => turn.key), [turns])
  const activeTurnKey = useActiveTurnKey(viewport, turnKeys)

  const liveTrace = (run: ConversationTimelineRun | null) => (
    <RunTrace
      key={run ? run.id : "live"}
      run={run}
      live={hasActiveStream ? { steps: stream.steps, startedAt: liveStartedAt ?? undefined } : null}
      attention={
        (!!run && runsAwaitingUser.has(run.id)) ||
        (!!activeRunId && runsAwaitingUser.has(activeRunId)) ||
        stream.pendingInteraction?.kind === "permission"
      }
    />
  )
  const traceFor = (run: ConversationTimelineRun) => (
    <Fragment key={run.id}>
      {run.id === activeRunId
        ? liveTrace(run)
        : <RunTrace run={run} live={null} attention={runsAwaitingUser.has(run.id)} />}
      {(run.status === "failed" || run.status === "interrupted") && (
        <RunFailureNotice
          run={run}
          workspaceID={convWorkspaceId}
          canRetry={canWrite && !activeRunId}
          onRunStarted={(runID) => {
            startRun(runID)
            void timelineQ.refetch()
          }}
        />
      )}
    </Fragment>
  )

  // When the stream finishes, refetch the timeline so the persisted
  // assistant message replaces the in-memory deltaText, then drop the
  // activeRunId so polling resumes for any follow-up runs. We treat
  // status="error" the same way: the stream has terminated either
  // cleanly (done) or with a hang/connection error, and in both cases
  // we must clear activeRunId — otherwise ComposerForm's stop button
  // (showStop depends on !!activeRunId) stays stuck on the square
  // icon forever even though no run is actually in flight.
  useEffect(() => {
    if (!activeRunId) return
    if (stream.status !== "done" && stream.status !== "error") return
    qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", conversationId] })
    const timer = window.setTimeout(() => {
      if (stream.status === "error" && streamErrorMessage) {
        // activeRunId is cleared below, which resets the stream hook to idle.
        // Keep a durable, dismissible copy so a fast provider rejection does
        // not disappear before the user can read it and look like a stalled run.
        setChatToast({ ...streamErrorMessage, runID: activeRunId })
      }
      setActiveRunId(null)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [stream.status, streamErrorMessage, activeRunId, conversationId, qc])

  return (
    <div ref={cancelScopeRef} className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
      {chrome !== "bare" && <ThreadHeader
        folded={folded}
        onExpand={onExpand}
        actions={
          <>
            <ListSlot slotId="conversation.header.actions" context={{ conversationId, agent }} />
          </>
        }
      />}

      <div className="relative flex min-h-0 flex-1 flex-col">
        {chrome !== "bare" && <TurnNavRail turns={turns} activeKey={activeTurnKey} onJump={scrollToTurn} />}
        <div ref={setViewport} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div ref={setThreadContent} className="mx-auto flex w-full max-w-[var(--thread-max-width)] flex-1 flex-col gap-5 px-4 py-6">
          {timelineQ.isLoading ? (
            <Skeleton className="h-16 w-3/4" />
          ) : messages.length === 0 ? (
            <p className="m-0 py-6 text-center text-sm text-fg-muted">
              {t("conversations.detail.emptyTimeline")}
            </p>
          ) : (
            messages.map((m) => {
              const row = (
                <MessageRow
                  senderType={m.sender_type}
                  messageType={m.kind}
                  content={m.content}
                  metadata={m.metadata}
                  outputRuns={runsByOutputMessage.get(m.id)}
                  stamp={fmtAgo(m.created_at)}
                  agentName={agent?.name || (m.sender_id === convInfoQ.data?.primary_agent_id ? agentName : "")}
                  workspaceID={convWorkspaceId}
                  onOpenRun={openRun}
                />
              )
              if (isUserMessageSender(m.sender_type)) {
                // The turn and the work it triggered share one anchor.
                return (
                  <div key={m.id} data-turn-key={m.id} className="flex flex-col gap-3">
                    {row}
                    {(runsByTrigger.get(m.id) ?? []).map(traceFor)}
                  </div>
                )
              }
              return (
                <Fragment key={m.id}>
                  {(orphanRunsByOutput.get(m.id) ?? []).map(traceFor)}
                  {row}
                  {(runsByTrigger.get(m.id) ?? []).map(traceFor)}
                </Fragment>
              )
            })
          )}
          {unanchoredFailures.map(traceFor)}
          {activeRunId && !liveRunAnchored && liveTrace(null)}
          {hasActiveStream && stream.deltaText && (
            <MessageRow
              senderType="agent"
              content={stream.deltaText}
              stamp={t("conversations.stream.caretHint")}
              agentName={agentName}
              workspaceID={convWorkspaceId}
            />
          )}
          <ConversationInteractionCards
            workspaceID={convWorkspaceId}
            conversationID={conversationId}
            preferredRequestID={
              stream.pendingInteraction?.kind === "user_choice" ? stream.pendingInteraction.requestId : undefined
            }
            omitPermission
          />
          {stream.status === "error" && (
            <ErrorState appearance="panel" title={streamErrorMessage?.text} detail={streamErrorMessage?.detail} />
          )}
          {/*
            Queued runs render one "queued" line per run, distinct from the
            in-flight working/thinking indicator above. Mirrors the Feishu
            queue-card driver behaviour (one line per blocked message).
            Position is the timeline snapshot — same staleness budget as
            the surrounding 5-second polling.
          */}
          {runs
            .filter((r) => r.status === "queued")
            .map((r) => (
              <p key={r.id} className="m-0 flex items-center gap-2 text-sm text-fg">
                <StatusIcon status="queued" />
                <span>
                  {r.queue_position && r.queue_position > 1
                    ? t("conversations.stream.queuedWithPosition", { position: r.queue_position })
                    : t("conversations.stream.queued")}
                </span>
              </p>
            ))}
        </div>
        </div>
        {showScrollToLatest && (
          <Button
            variant="outline"
            size="sm"
            shape="pill"
            className="absolute bottom-3 z-10 self-center"
            onClick={scrollToLatest}
          >
            <ArrowDown strokeWidth={1.5} aria-hidden="true" />
            {t("conversations.scrollToLatest")}
          </Button>
        )}
      </div>

      {cancelRunMut.error && !runs.some((run) => run.id === cancelRunMut.variables?.runID && run.status === "cancelled") && <ErrorDialog
        title={t("conversations.composer.stopErrorTitle")}
        message={t("conversations.composer.stopError")}
        detail={cancelRunMut.error.message}
        onClose={() => cancelRunMut.reset()}
        focusScopeRef={cancelScopeRef}
        onRestoreFocus={() => {
          const scope = cancelScopeRef.current
          const target = scope?.querySelector<HTMLButtonElement>(`button[aria-label="${CSS.escape(t("conversations.composer.stopAria"))}"]`)
            ?? scope?.querySelector<HTMLTextAreaElement>("textarea")
          target?.focus()
        }}
      />}
      <ComposerFooter>
        <ListSlot slotId="conversation.input.dock" context={{ conversationId }} />
        {chatToast && !runs.some((run) => run.id === chatToast.runID && (run.status === "failed" || run.status === "interrupted")) && (
          <ChatErrorToast message={chatToast} onDismiss={() => setChatToast(null)} />
        )}
        {pendingPermissions.length > 0 && convWorkspaceId ? (
          // The approval takes the composer's slot: one control for the
          // decision, none for typing until the agent may continue.
          <ApprovalBar
            interactions={pendingPermissions}
            workspaceID={convWorkspaceId}
            stop={
              activeRunId
                ? {
                    label: t("conversations.composer.stopAria"),
                    pending: cancelRunMut.isPending,
                    onStop: () => {
                      const runID = activeRunId
                      setActiveRunId(null)
                      cancelRunMut.mutate({ runID, reason: "user_clicked_stop" })
                    },
                  }
                : undefined
            }
          />
        ) : (
          <ComposerForm
            conversationId={conversationId}
            agentName={agentName}
            placeholder={agentDeleted ? t("agents.deletedLabel") : t("conversations.composer.placeholder", { agent: agentName })}
            disabled={!canWrite || !agent || agentDeleted || sandboxGuard?.blocked}
            onAfterSend={async () => scrollToLatest()}
            onRunStarted={startRun}
            onStartError={(message: string) => setChatToast({ text: message })}
            activeRunId={activeRunId}
            // Drop activeRunId immediately on click for the same reason
            // the "Cancel all" header button does: stop showing "thinking" /
            // the in-progress affordance the moment the user asks for
            // it, instead of waiting for the daemon to acknowledge the
            // abort. Server-side useCancelRun handles the actual run
            // cancellation + connector.Abort.
            onCancelActiveRun={
              canWrite && activeRunId
                ? () => {
                    const runID = activeRunId
                    setActiveRunId(null)
                    cancelRunMut.mutate({ runID, reason: "user_clicked_stop" })
                  }
                : undefined
            }
            cancelling={cancelRunMut.isPending}
            blockReason={agentDeleted ? t("conversations.composer.agentDeleted") : !canWrite ? t("conversations.composer.readOnly") : sandboxGuard?.blocked ? sandboxGuard.message : undefined}
          />
        )}
      </ComposerFooter>
    </div>
  )
}

/* ============================================================== */
/*  Run trace — timeline / SSE steps normalised for WorkTrace        */
/* ============================================================== */

const RUN_STATUS_KINDS: readonly StatusKind[] = [
  "queued",
  "running",
  "completed",
  "failed",
  "cancelled",
  "interrupted",
]

function runStatusKind(run: ConversationTimelineRun): StatusKind {
  if ((RUN_STATUS_KINDS as readonly string[]).includes(run.status)) return run.status as StatusKind
  return run.started_at ? "completed" : "queued"
}

function isoMs(iso?: string): number | undefined {
  if (!iso) return undefined
  const ms = Date.parse(iso)
  return isNaN(ms) ? undefined : ms
}

function timelineTraceSteps(run: ConversationTimelineRun): TraceStep[] {
  return (run.steps ?? []).map((s) => ({
    id: s.tool_call_id,
    name: s.name,
    // A completed tool event may record failure even when the run succeeds.
    status: isFailedToolResult(s.result) || (run.status === "failed" && s.status === "running") ? "failed" : s.status,
    args: s.args,
    result: s.result,
    startedAt: isoMs(s.occurred_at),
  }))
}

/**
 * SSE steps carry page-relative clocks and no results. Replayed events for a
 * step the timeline already knows borrow its wall-clock start and result, so
 * a resumed run does not restart every counter at the moment of subscribing.
 */
function streamTraceSteps(steps: StreamingStep[], known: ToolStep[]): TraceStep[] {
  const origin = performance.timeOrigin
  const byId = new Map(known.map((s) => [s.tool_call_id, s]))
  return steps.map((s) => {
    const persisted = byId.get(s.tool_call_id)
    const persistedStart = isoMs(persisted?.occurred_at)
    return {
      id: s.tool_call_id,
      name: s.name,
      status: s.status,
      args: s.args ?? persisted?.args,
      result: persisted?.result,
      startedAt: persistedStart ?? origin + s.started_at,
      endedAt: persistedStart !== undefined ? undefined : s.ended_at === undefined ? undefined : origin + s.ended_at,
    }
  })
}

/**
 * One run's WorkTrace. `live` carries the SSE steps while this run streams
 * (they replay the persisted events and then follow, so they supersede the
 * timeline snapshot); `run` is null only for a run the timeline has not
 * caught up with yet. Cancelled runs retain a neutral status without steps.
 */
function RunTrace({
  run,
  live,
  attention,
}: {
  run: ConversationTimelineRun | null
  live: { steps: StreamingStep[]; startedAt?: number } | null
  attention: boolean
}) {
  const status: StatusKind = live ? "running" : run ? runStatusKind(run) : "running"
  const steps = useMemo(() => {
    if (live && live.steps.length > 0) return streamTraceSteps(live.steps, run?.steps ?? [])
    return run ? timelineTraceSteps(run) : []
  }, [live, run])
  if (status !== "running" && status !== "queued" && status !== "cancelled" && steps.length === 0) return null
  if (status === "queued" && !live) return null
  return (
    <WorkTrace
      steps={steps}
      status={status}
      startedAt={isoMs(run?.started_at) ?? live?.startedAt}
      finishedAt={isoMs(run?.finished_at)}
      attentionRequired={attention}
    />
  )
}

function ChatErrorToast({ message, onDismiss }: { message: { text: string; detail?: string }; onDismiss: () => void }) {
  const { t: tc } = useTranslation("common")
  return (
    <div className="relative mb-2">
      <ErrorState appearance="panel" className="pr-10" title={message.text} detail={message.detail} />
      <Button variant="ghost" size="icon" className="absolute right-2 top-2 h-6 w-6" onClick={onDismiss} aria-label={tc("actions.close")}>
        <X strokeWidth={1.5} aria-hidden="true" />
      </Button>
    </div>
  )
}

function isContextRejectedStreamError(error: string): boolean {
  const normalized = error.toLowerCase()
  return (
    normalized.includes("inappropriate content") ||
    normalized.includes("content rejected") ||
    normalized.includes("content policy") ||
    (normalized.includes("invalidparameter") && normalized.includes("input data"))
  )
}

const MessageRow = memo(function MessageRow({
  senderType,
  messageType,
  content,
  metadata,
  outputRuns,
  stamp,
  agentName,
  workspaceID,
  onOpenRun,
}: {
  senderType: string
  messageType?: string
  content: string
  metadata?: Record<string, unknown>
  outputRuns?: ConversationTimelineRun[]
  stamp: string
  agentName: string
  workspaceID: string | null
  onOpenRun?: (runID: string) => void
}) {
  const { i18n, t } = useTranslation("admin")
  const isUser = isUserMessageSender(senderType)
  if (isUser) {
    return (
      <div className="flex justify-end">
        <div className="flex max-w-[85%] flex-col items-end">
          <div className="rounded-md bg-surface-muted px-3 py-2 text-base text-fg">
            <p className="m-0 whitespace-pre-wrap break-words">{content}</p>
          </div>
          <div className="mt-1 text-xs text-fg-muted">
            {senderType === "external" && <>{t("conversations.detail.externalUser")} · </>}{stamp}
          </div>
        </div>
      </div>
    )
  }
  const senderName = senderType === "system" ? t("conversations.detail.systemSender") : agentName
  const byline = senderName ? `${senderName} · ${stamp}` : stamp
  if (isRuntimeCapabilityError(messageType, metadata)) {
    const runtimeError = runtimeErrorViewModel(metadata, content, workspaceID, i18n.language, t)
    return (
      <div className="max-w-[85%]">
        <div className="mb-1 text-xs text-fg-muted">{byline}</div>
        <ErrorState
          appearance="panel"
          announce={false}
          title={runtimeError.message}
          hint={t("conversations.runtime_error.retryHint")}
          action={runtimeError.href && runtimeError.action ? (
            <Button asChild variant="outline" size="sm">
              <a href={runtimeError.href}>{runtimeError.action}</a>
            </Button>
          ) : undefined}
        />
      </div>
    )
  }
  const allSteps: ToolStep[] = (outputRuns ?? []).flatMap((r) => {
    const steps = r.steps ?? []
    // Server doesn't emit step.status="failed" — when a run fails before a
    // tool emits tool.result, its step stays "running" forever (see
    // store.buildToolSteps). Surface it as failed so the trace doesn't show
    // a perpetual spinner. See store.ToolStepRead for the upstream root fix.
    if (r.status !== "failed") return steps
    return steps.map((s) => (s.status === "running" ? { ...s, status: "failed" as const } : s))
  })
  const failedRun = (outputRuns ?? []).find((r) => r.status === "failed")
  // Extract presentation from: 1) message metadata, or 2) tool step results.
  // Plugin-host embeds __parsar_presentation in the MCP tool_result content
  // blocks; the daemon forwards it in the step result.content array.
  const presentation = (metadata?.presentation as { kind?: string; data?: unknown } | undefined)
    ?? extractPresentationFromSteps(allSteps)
  return (
    <div className="max-w-[85%]">
      <div className="mb-1 text-xs text-fg-muted">{byline}</div>
      <ToolCardSlot
        presentation={presentation}
        content={content}
        fallback={senderType === "agent"
          ? <MessageMarkdown content={content} />
          : <p className="m-0 whitespace-pre-wrap break-words text-base text-fg">{content}</p>}
      />
      {failedRun && onOpenRun && (
        <Button variant="link" size="sm" className="mt-1 px-0" onClick={() => onOpenRun(failedRun.id)}>
          {t("conversations.detail.viewRunLink")}
          <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
        </Button>
      )}
    </div>
  )
})

function runtimeErrorViewModel(
  metadata: Record<string, unknown> | undefined,
  fallback: string,
  workspaceID: string | null,
  language: string,
  t: ReturnType<typeof useTranslation<"admin">>["t"],
) {
  const subKind =
    stringMeta(metadata, "sub_kind") || stringMeta(metadata, "payload.sub_kind") || fallback
  const capabilityName =
    stringMeta(metadata, "capability_name") || t("conversations.runtime_error.fallbackCapability")
  const capabilityID = stringMeta(metadata, "capability_id")
  const credentialKind = stringMeta(metadata, "credential_kind")
  const kindLabel = credentialKindLabel(
    credentialKind,
    language,
    credentialKind || t("myCredentials.kind.unknown"),
  )
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`
  const { credential: href, capability: manageCapabilityHref } = conversationRecoveryLinks(workspaceID, capabilityID, credentialKind, current)

  switch (subKind) {
    case "capability_credential_missing":
      return {
        message: t("conversations.runtime_error.capability_credential_missing", {
          name: capabilityName,
          kind: kindLabel,
        }),
        action: t("conversations.runtime_error.addCredential"),
        href,
      }
    case "capability_credential_decrypt_failed":
      return {
        message: t("conversations.runtime_error.capability_credential_decrypt_failed", {
          name: capabilityName,
        }),
        action: "",
        href: "",
      }
    case "capability_credential_kind_mismatch":
      return {
        message: t("conversations.runtime_error.capability_credential_kind_mismatch", {
          name: capabilityName,
        }),
        action: t("conversations.runtime_error.resetCredential"),
        href,
      }
    case "capability_version_unavailable":
      // Daemon resolver couldn't find a usable zip (empty oss_key) for
      // either the pinned version or the latest version. Direct the
      // user to the capability detail page where they can re-upload or
      // pick a different version. No credential `href`, but
      // manageCapabilityHref is always populated.
      return {
        message: t("conversations.runtime_error.capability_version_unavailable", {
          name: capabilityName,
        }),
        action: t("conversations.runtime_error.manageCapability"),
        href: manageCapabilityHref,
      }
    default:
      return { message: fallback || t("conversations.runtime_error.generic"), action: "", href: "" }
  }
}

function stringMeta(metadata: Record<string, unknown> | undefined, key: string): string {
  if (!metadata) return ""
  const value = key.includes(".")
    ? key
        .split(".")
        .reduce<unknown>(
          (acc, part) =>
            acc && typeof acc === "object" ? (acc as Record<string, unknown>)[part] : undefined,
          metadata,
        )
    : metadata[key]
  return typeof value === "string" ? value : ""
}

/* ============================================================== */
/*  Composer — Textarea, Enter to send, Shift+Enter for a newline   */
/* ============================================================== */

function ComposerForm({
  conversationId,
  agentId,
  placeholder,
  disabled,
  autoFocus,
  onSendDirect,
  onAfterSend,
  onRunStarted,
  onStartError,
  activeRunId,
  onCancelActiveRun,
  cancelling,
  blockReason,
  agentName,
}: {
  conversationId: string
  agentId?: string
  placeholder: string
  /** Name of the agent that receives the message; shown in the toolbar. */
  agentName?: string
  disabled?: boolean
  autoFocus?: boolean
  /**
   * Optional override used by the empty-state composer. When set, the form
   * calls this instead of the conversationId-scoped send hook. Lets the
   * parent atomically createConversation + sendUserMessage + navigate.
   */
  onSendDirect?: (content: string) => Promise<boolean>
  onAfterSend?: (title: string) => Promise<void>
  /**
   * Called after a successful send with the dispatched agent_run_id (if any).
   * The parent uses this to open an SSE subscription for the streaming
   * assistant reply. Not invoked in onSendDirect mode — the empty-state
   * caller navigates away and the destination view subscribes on mount.
   */
  onRunStarted?: (runId: string) => void
  /**
   * Called when the fire-and-forget POST /runs/{id}/start call fails. The
   * server now auto-starts agent_daemon runs (StreamingDispatcher), so the
   * /start POST here is a tolerant fallback: a 200 on `already running` is
   * normal, but a network error / 5xx still means the run won't progress
   * and the user needs to see why. Parent renders this through a toast.
   */
  onStartError?: (message: string) => void
  /**
   * When non-null, a run is currently streaming for this conversation. The
   * trailing Send button morphs into a Stop button (Square icon) that
   * invokes onCancelActiveRun. This is the ChatGPT/Claude.ai-style "switch
   * Send for Stop while generating" affordance, complementary to the
   * conversation-header "Cancel all" — single-run cancel here keys off the
   * specific runId the composer just dispatched.
   */
  activeRunId?: string | null
  onCancelActiveRun?: () => void
  cancelling?: boolean
  blockReason?: string
}) {
  const { t } = useTranslation("admin")
  const [content, setContent] = useState("")
  const [busy, setBusy] = useState(false)
  // A send failure belongs to the conversation (or the agent, before one
  // exists) it was typed into, so switching away clears it (main #280).
  const [sendError, setSendError] = useState<{ targetId: string; message: string } | null>(null)
  const sendTargetId = conversationId || agentId || ""
  const inputRef = useRef<HTMLTextAreaElement | null>(null)
  const sendMut = useSendUserMessage(conversationId || null)

  useEffect(() => {
    if (!autoFocus || disabled) return
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0)
    return () => window.clearTimeout(timer)
  }, [autoFocus, disabled, conversationId])

  // Empty-state mode: send button is enabled even though conversationId is
  // empty, because onSendDirect handles the create-then-send flow.
  const trimmed = content.trim()
  const canSubmit =
    !disabled &&
    trimmed.length > 0 &&
    !sendMut.isPending &&
    !busy &&
    (onSendDirect ? true : !!conversationId)

  const submit = async () => {
    if (!canSubmit) return
    setSendError(null)
    const text = trimmed
    const handleSendError = (err: unknown) => {
      setSendError({ targetId: sendTargetId, message: err instanceof Error ? err.message : String(err) })
    }
    if (onSendDirect) {
      setBusy(true)
      try {
        const sentToCurrentConversation = await onSendDirect(text)
        if (sentToCurrentConversation) setContent("")
      } catch (err) {
        handleSendError(err)
      } finally {
        setBusy(false)
      }
    } else {
      const resp = await sendMut.mutateAsync({ content: text }).catch(handleSendError)
      if (!resp) return
      if (onAfterSend) await onAfterSend(text.slice(0, 30))
      setContent("")
      // Pick the first dispatched run id (1v1 currently dispatches at most
      // one). The server-side StreamingDispatcher auto-starts agent_daemon
      // runs at message-commit time, so this /start POST is a tolerant
      // fallback: server returns 200 on `already running`, which we treat
      // as success. A real failure (network/5xx) still needs to surface —
      // otherwise the user sees their message land and nothing else
      // happens.
      const runId = resp.agent_run_id ?? resp.run_ids?.[0] ?? null
      if (runId && onRunStarted) {
        onRunStarted(runId)
        void startAgentRun(conversationId, runId).catch((err) => {
          onStartError?.(err instanceof Error ? err.message : String(err))
        })
      }
    }
  }

  const isBusy = busy || sendMut.isPending
  // While the conversation has an in-flight run AND the user hasn't typed
  // anything yet, the trailing button morphs from Send → Stop. Typing
  // overrides — letting users queue the next prompt while the current
  // generation finishes mirrors the "/cancel" + "new message" coexistence in
  // the Feishu side. Empty-state composer (onSendDirect) never shows
  // Stop because no run is in flight there.
  const showStop =
    !onSendDirect && !!activeRunId && !!onCancelActiveRun && trimmed.length === 0 && !isBusy

  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      {sendError?.targetId === sendTargetId && (
        <ChatErrorToast message={{ text: sendError.message }} onDismiss={() => setSendError(null)} />
      )}
      {blockReason && <InlineError className="mb-2">{blockReason}</InlineError>}
      {/* The composer: one tonal, borderless 16px panel (the ChatGPT idiom
          in our greys), text on top, a toolbar row below with the bound
          agent and the round ink send / stop button. */}
      <div className="rounded-2xl bg-surface-muted px-4 pb-3 pt-4">
        <textarea
          ref={inputRef}
          rows={1}
          value={content}
          onChange={(e) => {
            setContent(e.target.value)
            // Grow with the text up to eight lines, then scroll.
            const el = e.currentTarget
            el.style.height = "auto"
            el.style.height = `${Math.min(el.scrollHeight, 200)}px`
          }}
          onKeyDown={(e) => {
            // Enter sends; Shift+Enter inserts a newline; never fire mid-IME.
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              void submit()
            }
          }}
          placeholder={placeholder}
          aria-label={t("conversations.composer.label")}
          disabled={disabled || (!conversationId && !onSendDirect)}
          className="block max-h-[200px] min-h-[40px] w-full resize-none bg-transparent text-base leading-relaxed text-fg placeholder:text-fg-muted focus:outline-none disabled:cursor-not-allowed disabled:opacity-60"
        />
        <div className="mt-2 flex h-8 items-center justify-end gap-3">
          {agentName && (
            <span className="flex min-w-0 items-center gap-1.5 text-xs text-fg-muted">
              <InitialTile name={agentName} />
              <span className="truncate">{agentName}</span>
            </span>
          )}
          {showStop ? (
            <button
              type="button"
              onClick={onCancelActiveRun}
              disabled={cancelling}
              aria-label={t("conversations.composer.stopAria")}
              title={t("conversations.composer.stopAria")}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis active:scale-[0.97] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50"
            >
              {cancelling ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
              ) : (
                <Square className="h-3 w-3 fill-current" strokeWidth={1.5} aria-hidden="true" />
              )}
            </button>
          ) : (
            <button
              type="submit"
              disabled={!canSubmit}
              aria-label={isBusy ? t("conversations.composer.sending") : t("conversations.composer.send")}
              title={t("conversations.composer.send")}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis transition-opacity duration-150 ease-settle active:scale-[0.97] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-30"
            >
              {isBusy ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
              ) : (
                <ArrowUp className="h-4 w-4" strokeWidth={2} aria-hidden="true" />
              )}
            </button>
          )}
        </div>
      </div>
    </form>
  )
}

/* ============================================================== */
/*  Utilities                                                        */
/* ============================================================== */

/**
 * Extract __parsar_presentation from tool step results.
 * Plugin-host embeds presentation as a content block in the MCP tool_result:
 *   result.content = [{type:'text', text:'...'}, {type:'text', text:'{"__parsar_presentation":...}'}]
 * Returns the first presentation found across all steps, or undefined.
 */
function extractPresentationFromSteps(steps: ToolStep[]): { kind?: string; data?: unknown } | undefined {
  for (const step of steps) {
    if (!step.result) continue
    const content = step.result.content
    if (!Array.isArray(content)) continue
    for (const block of content) {
      if (typeof block !== "object" || block === null) continue
      const text = (block as { text?: string }).text
      if (typeof text !== "string") continue
      if (!text.includes("__parsar_presentation")) continue
      try {
        const parsed = JSON.parse(text)
        if (parsed.__parsar_presentation) {
          return parsed.__parsar_presentation as { kind?: string; data?: unknown }
        }
      } catch {
        // Not valid JSON, skip.
      }
    }
  }
  return undefined
}

/** Distinguishing tail of a long id: the prefix is shared, the tail is not. */
export function tailId(s?: string, n = 8): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(-n)
}
