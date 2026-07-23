import { useQueryClient } from "@tanstack/react-query"
import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import {
  AlertTriangle,
  Bot,
  Check,
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  CircleDot,
  Clock,
  MessageSquarePlus,
  Pencil,
  Send,
  ShieldAlert,
  Square,
  Trash2,
  X,
  Loader2,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { ConversationInteractionCards } from "../../components/conversation/ConversationInteractionCards"
import { WorkingSteps, StepTrace } from "../../components/conversation/StepDisplay"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Skeleton } from "../../components/ui/skeleton"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAgents, useCancelRun, useCancelConversation } from "../../lib/api-agents"
import { agentNeedsSandbox } from "../../lib/agent-runtime"
import {
  createConversation,
  sendUserMessage,
  startAgentRun,
  useAgentRunStream,
  useConversation,
  useConversationTimeline,
  useDeleteConversation,
  useConversations,
  useSendUserMessage,
  useUpdateConversationTitle,
} from "../../lib/api-conversations"
import { useSandboxBinding, type SandboxBinding } from "../../lib/api-sandbox"
import type {
  ConversationListItem,
  ConversationTimelineRun,
  Agent,
  ToolStep,
} from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"
import {
  forgetConversationViewConversation,
  readConversationViewState,
  writeConversationViewState,
} from "../../lib/conversation-view-state"
import { credentialKindLabel } from "./capability-ui"
import {
  dedupeCapabilityRuntimeDiagnostics,
  isRuntimeErrorMessage,
  stringMeta,
} from "./conversation-runtime-errors"

const FOLD_KEY = "parsar:conv:sidebarFolded"

interface SandboxSendGuard {
  blocked: boolean
  message: string
}

/* ============================================================== */
/*  ConversationsPage — top-level: 3-col shell (admin | conv | main) */
/* ============================================================== */

export function ConversationsPage() {
  const { t } = useTranslation("admin")
  const { entityId, navigate } = useAdminView()
  const focusTarget = new URLSearchParams(window.location.search).get("focus")
  const wsId = useWorkspaceId()
  const restoreViewState = !entityId && focusTarget !== "compose"
  const savedViewState = useMemo(() => readConversationViewState(wsId), [wsId])

  const agentsQ = useAgents(wsId)
  const allAgents: Agent[] = useMemo(
    () => (agentsQ.data?.agents ?? []).filter((a) => a.status === "active"),
    [agentsQ.data],
  )

  // Sidebar selection follows the active conv's primary_agent_id; when
  // there's no active conv, fall back to user pick / first active agent.
  const currentConvQ = useConversation(entityId ?? null, wsId)
  const currentConv = currentConvQ.data

  // Selected agent: current conv's primary_agent_id → user pick →
  // first active agent → "".
  const [pickedAgent, setPickedAgent] = useState<{
    workspaceId: string | null
    agentId: string | null
  }>({
    workspaceId: null,
    agentId: null,
  })

  useEffect(() => {
    if (!wsId || !restoreViewState) return
    const saved = readConversationViewState(wsId)
    if (saved.conversationId) {
      navigate("conversations", { id: saved.conversationId })
    }
  }, [wsId, restoreViewState, navigate])

  const pickedAgentId =
    pickedAgent.workspaceId === wsId && pickedAgent.agentId
      ? pickedAgent.agentId
      : savedViewState.agentId
  const selectedAgentId = currentConv?.primary_agent_id || pickedAgentId || (allAgents[0]?.id ?? "")
  const selectedAgent = allAgents.find((a) => a.id === selectedAgentId)
  const needsSandbox = agentNeedsSandbox(selectedAgent)
  const sandboxQ = useSandboxBinding(
    needsSandbox ? wsId : null,
    needsSandbox ? selectedAgentId : null,
  )
  const sandboxGuard = useMemo(
    () => sandboxSendGuard(t, selectedAgent, sandboxQ.data, sandboxQ.isLoading, sandboxQ.error),
    [t, selectedAgent, sandboxQ.data, sandboxQ.isLoading, sandboxQ.error],
  )

  useEffect(() => {
    if (!wsId || !selectedAgentId) return
    writeConversationViewState(wsId, { agentId: selectedAgentId })
  }, [wsId, selectedAgentId])

  useEffect(() => {
    if (!wsId || !entityId) return
    writeConversationViewState(wsId, {
      agentId: currentConv?.primary_agent_id ?? selectedAgentId ?? null,
      conversationId: entityId,
    })
  }, [wsId, entityId, currentConv?.primary_agent_id, selectedAgentId])

  useEffect(() => {
    if (!wsId || !entityId) return
    if (!(currentConvQ.error instanceof ApiError)) return
    if (currentConvQ.error.envelope.status !== 404) return
    const saved = readConversationViewState(wsId)
    if (saved.conversationId !== entityId) return
    forgetConversationViewConversation(wsId, entityId)
    navigate("conversations", { id: "", focus: "compose" })
  }, [wsId, entityId, currentConvQ.error, navigate])

  // Sidebar conversations: scoped to the selected agent.
  const convsQ = useConversations(wsId, selectedAgentId)
  const conversations: ConversationListItem[] = convsQ.data?.conversations ?? []

  // Fold state — persisted, defaults to expanded.
  const [folded, setFolded] = useState<boolean>(() => {
    try {
      return localStorage.getItem(FOLD_KEY) === "1"
    } catch {
      return false
    }
  })
  const toggleFold = () => {
    setFolded((v) => {
      const next = !v
      try {
        localStorage.setItem(FOLD_KEY, next ? "1" : "0")
      } catch {
        /* ignore */
      }
      return next
    })
  }

  const qc = useQueryClient()

  // "New conversation" navigates to an empty composer without pre-creating a
  // conv — the conv is created on first send via handleSendFromEmpty,
  // so the sidebar only shows rows with a real first user turn.
  const openCreate = () => {
    navigate("conversations", { id: "", focus: "compose" })
  }

  // First-send creates the conv + posts the message + navigates in.
  // Title derives from the first 30 chars so the sidebar gets a
  // meaningful name immediately (server defaults to "Untitled conversation").
  const handleSendFromEmpty = async (content: string): Promise<void> => {
    if (!wsId || !selectedAgentId) {
      throw new Error("workspace_id and agent_id required for empty-state send")
    }
    const conv = await createConversation(wsId, {
      title: content.slice(0, 30),
      surface: "web",
      form: "thread",
      agent_id: selectedAgentId,
    })
    try {
      await sendUserMessage(conv.id, { content })
    } finally {
      // Invalidate even on first-message failure — the empty conv is
      // still real and the user can retry from the chat view.
      qc.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === "admin" && q.queryKey[1] === "conversations" && q.queryKey[2] === wsId,
      })
      qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", conv.id] })
    }
    writeConversationViewState(wsId, {
      agentId: selectedAgentId,
      conversationId: conv.id,
    })
    navigate("conversations", { id: conv.id })
  }

  const renameMutation = useUpdateConversationTitle(wsId)
  const deleteMutation = useDeleteConversation(wsId)
  const handleRenameConversation = async (cid: string, title: string): Promise<void> => {
    await renameMutation.mutateAsync({ cid, title })
  }
  const handleDeleteConversation = async (cid: string): Promise<void> => {
    await deleteMutation.mutateAsync(cid)
    forgetConversationViewConversation(wsId, cid)
    // If we just deleted the active conv, navigate away — otherwise
    // useConversation 404s and the UI jumps to EmptyChat.
    if (cid === entityId) {
      navigate("conversations", { id: "", focus: "compose" })
    }
  }

  return (
    <AdminLayout activeMenu="conversations" fullBleed>
      <div className="flex h-[calc(100vh-65px)] min-h-0">
        {!folded && (
          <ConversationSidebar
            agents={allAgents}
            selectedAgentId={selectedAgentId}
            onPickAgent={(id) => {
              setPickedAgent({ workspaceId: wsId, agentId: id })
              writeConversationViewState(wsId, { agentId: id })
              navigate("conversations", { id: "", focus: "compose" })
            }}
            agentsLoading={agentsQ.isLoading}
            conversations={conversations}
            selectedConversationId={entityId ?? ""}
            onPickConversation={(id) => {
              writeConversationViewState(wsId, {
                agentId: selectedAgentId,
                conversationId: id,
              })
              navigate("conversations", { id })
            }}
            convsLoading={convsQ.isLoading}
            onNewConversation={openCreate}
            onFold={toggleFold}
            onRenameConversation={handleRenameConversation}
            onDeleteConversation={handleDeleteConversation}
          />
        )}
        <ConversationMain
          conv={currentConv}
          convLoading={currentConvQ.isLoading}
          convError={currentConvQ.error}
          agent={selectedAgent}
          conversationId={entityId ?? ""}
          messageCount={conversations.find((c) => c.id === entityId)?.message_count ?? 0}
          folded={folded}
          onExpand={toggleFold}
          onPageDescription={t("conversations.page.description")}
          onSendFromEmpty={handleSendFromEmpty}
          onRenameAfterFirstMessage={handleRenameConversation}
          focusComposer={focusTarget === "compose"}
          sandboxGuard={sandboxGuard}
        />
      </div>
    </AdminLayout>
  )
}

function sandboxSendGuard(
  t: TFunction<"admin">,
  agent: Agent | undefined,
  binding: SandboxBinding | null | undefined,
  loading: boolean,
  error: unknown,
): SandboxSendGuard | undefined {
  if (!agentNeedsSandbox(agent)) return undefined
  if (loading) {
    return { blocked: true, message: t("conversations.sandboxGuard.checking") }
  }
  if (error) {
    const detail =
      error instanceof Error ? error.message : t("conversations.sandboxGuard.errorFallback")
    return { blocked: true, message: t("conversations.sandboxGuard.error", { error: detail }) }
  }
  if (!binding) {
    return { blocked: true, message: t("conversations.sandboxGuard.missing") }
  }
  if (binding.status_kind !== "live") {
    return {
      blocked: true,
      message: t("conversations.sandboxGuard.notLive", { status: binding.status }),
    }
  }
  return { blocked: false, message: "" }
}

/* ============================================================== */
/*  Sidebar (column 2)                                              */
/* ============================================================== */

interface SidebarProps {
  agents: Agent[]
  selectedAgentId: string
  onPickAgent: (id: string) => void
  agentsLoading: boolean
  conversations: ConversationListItem[]
  selectedConversationId: string
  onPickConversation: (id: string) => void
  convsLoading: boolean
  onNewConversation: () => void
  onFold: () => void
  onRenameConversation: (cid: string, title: string) => Promise<void>
  onDeleteConversation: (cid: string) => Promise<void>
}

function ConversationSidebar(p: SidebarProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const [pickerOpen, setPickerOpen] = useState(false)
  const selectedAgent = p.agents.find((a) => a.id === p.selectedAgentId)

  const [renamingConvId, setRenamingConvId] = useState<string | null>(null)
  const [renameDraft, setRenameDraft] = useState<string>("")
  const [renameError, setRenameError] = useState<string>("")
  const [renameBusy, setRenameBusy] = useState(false)
  const [deleteConvId, setDeleteConvId] = useState<string | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState<string>("")
  const renameInputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (renamingConvId && renameInputRef.current) {
      renameInputRef.current.focus()
      renameInputRef.current.select()
    }
  }, [renamingConvId])

  const startRename = (c: ConversationListItem) => {
    setRenamingConvId(c.id)
    setRenameDraft(c.title || "")
    setRenameError("")
  }
  const cancelRename = () => {
    setRenamingConvId(null)
    setRenameDraft("")
    setRenameError("")
  }
  const commitRename = async () => {
    if (!renamingConvId) return
    const trimmed = renameDraft.trim()
    if (trimmed === "") {
      setRenameError(t("conversations.sidebar.renameEmpty"))
      return
    }
    if (trimmed.length > 200) {
      setRenameError(t("conversations.sidebar.renameTooLong"))
      return
    }
    setRenameBusy(true)
    try {
      await p.onRenameConversation(renamingConvId, trimmed)
      cancelRename()
    } catch (err) {
      setRenameError(err instanceof Error ? err.message : t("conversations.sidebar.renameFailed"))
    } finally {
      setRenameBusy(false)
    }
  }

  const deleteConv = p.conversations.find((c) => c.id === deleteConvId)
  const confirmDelete = async () => {
    if (!deleteConvId) return
    setDeleteBusy(true)
    setDeleteError("")
    try {
      await p.onDeleteConversation(deleteConvId)
      setDeleteConvId(null)
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : t("conversations.sidebar.deleteFailed"))
    } finally {
      setDeleteBusy(false)
    }
  }

  return (
    <aside className="relative flex w-[244px] shrink-0 flex-col border-r border-line/70 bg-surface-subtle/70 px-2.5 py-2.5">
      {/* Current agent header (click to switch) */}
      <button
        type="button"
        onClick={() => setPickerOpen((v) => !v)}
        className="flex w-full items-start gap-2.5 rounded-lg border border-transparent bg-surface/70 px-2.5 py-2 text-left shadow-[0_1px_1px_rgba(15,23,42,0.03)] transition-colors hover:border-line hover:bg-surface"
        aria-haspopup="listbox"
        aria-expanded={pickerOpen}
        aria-label={t("conversations.sidebar.switchAgent")}
      >
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex items-center gap-1">
            <span className="truncate text-base font-semibold text-fg">
              {selectedAgent?.name || t("conversations.sidebar.allAgentsHint")}
            </span>
            <ChevronDown className="h-3 w-3 shrink-0 text-fg-faint" strokeWidth={2} />
          </div>
          {selectedAgent && (
            <span className="mt-0.5 truncate text-xs text-fg-subtle">
              {selectedAgent.description || t("conversations.sidebar.currentAgentDesc")}
            </span>
          )}
        </div>
      </button>

      {pickerOpen && (
        <div
          role="listbox"
          className="absolute left-2 right-2 top-[54px] z-10 max-h-72 overflow-y-auto rounded-lg border border-line bg-surface shadow-lg"
        >
          {p.agentsLoading ? (
            <div className="p-3">
              <Skeleton className="h-8 w-full" />
            </div>
          ) : p.agents.length === 0 ? (
            <p className="p-3 text-sm text-fg-subtle">{t("conversations.sidebar.allAgentsHint")}</p>
          ) : (
            p.agents.map((a) => (
              <button
                key={a.id}
                type="button"
                role="option"
                aria-selected={a.id === p.selectedAgentId}
                onClick={() => {
                  p.onPickAgent(a.id)
                  setPickerOpen(false)
                }}
                className={cn(
                  "block w-full px-3 py-2 text-left text-sm transition-colors hover:bg-surface-subtle",
                  a.id === p.selectedAgentId && "bg-info-subtle text-info",
                )}
              >
                <div className="font-medium">{a.name}</div>
                {a.description && (
                  <div className="mt-0.5 truncate text-xs text-fg-subtle">{a.description}</div>
                )}
              </button>
            ))
          )}
        </div>
      )}

      {/* New conversation */}
      <button
        type="button"
        onClick={p.onNewConversation}
        disabled={!p.selectedAgentId}
        className={cn(
          "mt-2 flex h-9 w-full cursor-pointer items-center justify-center gap-2 rounded-lg bg-surface-emphasis px-2.5 text-base font-medium text-white shadow-sm transition-colors",
          "hover:bg-surface-emphasis",
          "disabled:cursor-not-allowed disabled:opacity-50",
        )}
      >
        <MessageSquarePlus className="h-4 w-4" strokeWidth={1.9} />
        {t("conversations.sidebar.newConversation")}
      </button>

      {/* Conversation list */}
      <div className="mt-1 flex-1 space-y-0.5 overflow-y-auto pb-1">
        {p.convsLoading ? (
          <div className="space-y-2 px-2 pt-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : p.conversations.length === 0 ? (
          <p className="px-3 pt-4 text-sm leading-relaxed text-fg-faint">
            {t("conversations.sidebar.emptyForAgent")}
          </p>
        ) : (
          p.conversations.map((c) => {
            const isActive = c.id === p.selectedConversationId
            const isRenaming = renamingConvId === c.id
            // div+role="button" not <button>, so rename/delete can nest.
            return (
              <div
                key={c.id}
                role="button"
                tabIndex={isRenaming ? -1 : 0}
                onClick={() => {
                  if (!isRenaming) p.onPickConversation(c.id)
                }}
                onKeyDown={(e) => {
                  if (isRenaming) return
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault()
                    p.onPickConversation(c.id)
                  }
                }}
                className={cn(
                  "group/row relative block w-full rounded-lg border px-2.5 py-2 text-left transition-colors",
                  isActive
                    ? "border-line bg-surface shadow-sm"
                    : "border-transparent hover:border-line hover:bg-surface/80",
                  isRenaming ? "cursor-default" : "cursor-pointer",
                )}
              >
                {isRenaming ? (
                  <div className="flex flex-col gap-1">
                    <Input
                      ref={renameInputRef}
                      value={renameDraft}
                      onChange={(e) => {
                        setRenameDraft(e.target.value)
                        if (renameError) setRenameError("")
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault()
                          void commitRename()
                        } else if (e.key === "Escape") {
                          e.preventDefault()
                          cancelRename()
                        }
                      }}
                      onClick={(e) => e.stopPropagation()}
                      disabled={renameBusy}
                      className="h-7 text-sm"
                      aria-label={t("conversations.sidebar.renameAria")}
                    />
                    <div className="flex items-center justify-end gap-1">
                      {renameError && (
                        <span className="mr-auto text-xs text-danger">{renameError}</span>
                      )}
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          cancelRename()
                        }}
                        disabled={renameBusy}
                        aria-label={t("conversations.sidebar.renameCancel")}
                        className="flex h-6 w-6 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-surface-muted hover:text-fg-muted disabled:opacity-50"
                      >
                        <X className="h-3.5 w-3.5" strokeWidth={2} />
                      </button>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          void commitRename()
                        }}
                        disabled={renameBusy}
                        aria-label={t("conversations.sidebar.renameCommit")}
                        className="flex h-6 w-6 items-center justify-center rounded-md text-info transition-colors hover:bg-info-subtle disabled:opacity-50"
                      >
                        {renameBusy ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <Check className="h-3.5 w-3.5" strokeWidth={2.25} />
                        )}
                      </button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className="flex items-center gap-1.5 pr-12">
                      {isActive && (
                        <CircleDot className="h-3 w-3 shrink-0 text-success" strokeWidth={2.4} />
                      )}
                      <div
                        className={cn(
                          "truncate text-sm",
                          isActive ? "font-semibold text-fg" : "font-medium text-fg-muted",
                        )}
                      >
                        {truncate(c.title || "", 18)}
                      </div>
                    </div>
                    <div className="mt-1 flex items-center gap-1.5 text-xs text-fg-faint">
                      <span className="min-w-0 flex-1 truncate">
                        {c.last_message_preview ||
                          (c.last_message_at ? fmtAgo(c.last_message_at) : fmtAgo(c.created_at))}
                      </span>
                    </div>
                    {/* Hover-only action cluster. opacity-0 → */}
                    {/* group-hover:opacity-100 keeps the resting state clean. */}
                    <div className="absolute right-2 top-1.5 flex items-center gap-0.5 opacity-0 transition-opacity group-hover/row:opacity-100">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          startRename(c)
                        }}
                        aria-label={t("conversations.sidebar.renameAria")}
                        className="flex h-6 w-6 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-surface-muted hover:text-fg-muted"
                      >
                        <Pencil className="h-3 w-3" strokeWidth={2} />
                      </button>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation()
                          setDeleteError("")
                          setDeleteConvId(c.id)
                        }}
                        aria-label={t("conversations.sidebar.deleteAria")}
                        className="flex h-6 w-6 items-center justify-center rounded-md text-fg-faint transition-colors hover:bg-danger-subtle hover:text-danger"
                      >
                        <Trash2 className="h-3 w-3" strokeWidth={2} />
                      </button>
                    </div>
                  </>
                )}
              </div>
            )
          })
        )}
      </div>

      <Dialog
        open={deleteConvId !== null}
        onOpenChange={(next) => {
          if (!next && !deleteBusy) setDeleteConvId(null)
        }}
      >
        <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
          <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5 pr-5">
            <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
              <ShieldAlert className="h-4 w-4" />
            </div>
            <div className="space-y-1.5">
              <DialogTitle className="text-sm">
                {t("conversations.sidebar.deleteConfirmTitle")}
              </DialogTitle>
              <DialogDescription className="text-sm leading-relaxed">
                {t("conversations.sidebar.deleteConfirmDesc", {
                  title: deleteConv?.title || t("conversations.detail.unnamed"),
                })}
              </DialogDescription>
            </div>
          </DialogHeader>
          <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
            {deleteError && <span className="mr-auto text-sm text-danger">{deleteError}</span>}
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteConvId(null)}
              disabled={deleteBusy}
            >
              {t("conversations.sidebar.deleteCancel")}
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => void confirmDelete()}
              disabled={deleteBusy}
            >
              {deleteBusy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("conversations.sidebar.deleteConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Fold handle: vertical edge, VS Code style */}
      <button
        type="button"
        onClick={p.onFold}
        aria-label={t("conversations.sidebar.foldAria")}
        className="absolute -right-[11px] top-1/2 z-20 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-full border border-line bg-surface text-fg-subtle opacity-0 shadow-sm transition-opacity hover:border-info-border hover:bg-info-subtle hover:text-info group-hover:opacity-100 [aside:hover_&]:opacity-100"
      >
        <ChevronLeft className="h-3.5 w-3.5" strokeWidth={2} />
      </button>
    </aside>
  )
}

/* ============================================================== */
/*  Main (column 3) — header + body (empty or stream) + composer    */
/* ============================================================== */

interface MainProps {
  conv: import("../../lib/api-types").Conversation | undefined
  convLoading: boolean
  convError: unknown
  agent: Agent | undefined
  conversationId: string
  /** From the list summary; single-conv GET doesn't include it. */
  messageCount: number
  folded: boolean
  onExpand: () => void
  onPageDescription: string
  /** Empty-state send: create conv + post first message + navigate. */
  onSendFromEmpty: (content: string) => Promise<void>
  onRenameAfterFirstMessage: (cid: string, title: string) => Promise<void>
  focusComposer?: boolean
  sandboxGuard?: SandboxSendGuard
}

function ConversationMain(p: MainProps) {
  const { t } = useTranslation("admin")
  const err = p.convError
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  return (
    <main className="relative flex min-w-0 flex-1 flex-col bg-surface-subtle">
      {p.folded && (
        <button
          type="button"
          onClick={p.onExpand}
          aria-label={t("conversations.sidebar.expandAria")}
          className="absolute left-3 top-3 z-10 flex h-7 w-7 items-center justify-center rounded-md border border-line bg-surface text-fg-subtle shadow-sm transition-colors hover:border-line-strong hover:bg-surface-subtle hover:text-fg"
        >
          <ChevronRight className="h-4 w-4" strokeWidth={2} />
        </button>
      )}

      {/* Body */}
      <div className="flex min-h-0 flex-1 flex-col">
        {err ? (
          <div className="flex-1 overflow-y-auto p-6">
            <ErrorState
              title={
                isUnreachable
                  ? t("conversations.loadError.unreachable.title")
                  : t("conversations.loadError.title")
              }
              description={
                isUnreachable
                  ? t("conversations.loadError.unreachable.description")
                  : err instanceof Error
                    ? err.message
                    : t("conversations.loadError.description")
              }
              hint={
                isUnreachable
                  ? t("conversations.loadError.unreachable.hint")
                  : t("conversations.loadError.hint")
              }
            />
          </div>
        ) : p.convLoading ? (
          <div className="flex-1 space-y-4 p-8">
            <Skeleton className="h-20 w-2/3" />
            <Skeleton className="h-20 w-3/4 ml-auto" />
            <Skeleton className="h-32 w-2/3" />
          </div>
        ) : !p.conversationId || !p.conv ? (
          <EmptyChat
            agent={p.agent}
            pageDescription={p.onPageDescription}
            onSendFromEmpty={p.onSendFromEmpty}
            focusComposer={p.focusComposer}
            sandboxGuard={p.sandboxGuard}
          />
        ) : p.messageCount === 0 ? (
          // 0 messages: keep the EmptyChat aurora so a new conv looks
          // like the initial no-conversation state until the first send.
          <EmptyChat
            agent={p.agent}
            pageDescription={p.onPageDescription}
            conversationId={p.conversationId}
            workspaceID={p.conv.workspace_id}
            onRenameAfterFirstMessage={p.onRenameAfterFirstMessage}
            focusComposer={p.focusComposer}
            sandboxGuard={p.sandboxGuard}
          />
        ) : (
          <ChatStream
            conversationId={p.conversationId}
            agent={p.agent}
            sidebarFolded={p.folded}
            sandboxGuard={p.sandboxGuard}
          />
        )}
      </div>
    </main>
  )
}

/* ============================================================== */
/*  Empty state — workbench surface + composer                    */
/* ============================================================== */

function EmptyChat({
  agent,
  pageDescription,
  conversationId,
  workspaceID,
  onSendFromEmpty,
  onRenameAfterFirstMessage,
  focusComposer,
  sandboxGuard,
}: {
  agent: Agent | undefined
  pageDescription: string
  /** When set, composer sends into this conv (in-chat flow). */
  conversationId?: string
  workspaceID?: string
  /** Create-then-send flow (required when conversationId is unset). */
  onSendFromEmpty?: (content: string) => Promise<void>
  onRenameAfterFirstMessage?: (cid: string, title: string) => Promise<void>
  focusComposer?: boolean
  sandboxGuard?: SandboxSendGuard
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  return (
    <div className="flex flex-1 flex-col overflow-y-auto bg-surface-subtle px-5 py-6 sm:px-8">
      <div className="mx-auto flex min-h-0 w-full max-w-4xl flex-1 flex-col">
        <div className="flex items-center justify-between gap-3 text-xs text-fg-subtle">
          <span className="inline-flex items-center gap-1.5 rounded-md border border-line bg-surface/80 px-2 py-1 font-medium shadow-sm">
            <Bot className="h-3.5 w-3.5 text-fg-subtle" strokeWidth={2} />
            {agent?.name || t("conversations.sidebar.allAgentsHint")}
          </span>
          <span className="rounded-md border border-success-border bg-success-subtle px-2 py-1 font-medium text-success">
            {t("conversations.empty.mode")}
          </span>
        </div>

        {conversationId && workspaceID ? (
          <div className="mt-5">
            <ConversationInteractionCards
              workspaceID={workspaceID}
              conversationID={conversationId}
              onOpenInbox={() => navigate("approvals")}
            />
          </div>
        ) : null}

        <div className="grid flex-1 place-items-center py-8">
          <div className="w-full max-w-3xl">
            <div className="mb-7 text-center">
              <h2 className="text-2xl font-semibold tracking-display text-fg">
                {t("conversations.empty.greet")}
              </h2>
              <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-fg-subtle">
                {pageDescription}
              </p>
            </div>

            <ComposerForm
              conversationId={conversationId ?? ""}
              disabled={!agent || sandboxGuard?.blocked}
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
          </div>
        </div>
      </div>
    </div>
  )
}

/* ============================================================== */
/*  Chat stream — user (right bubble) + agent (left plain text)     */
/* ============================================================== */

function ChatStream({
  conversationId,
  agent,
  sidebarFolded,
  sandboxGuard,
}: {
  conversationId: string
  agent: Agent | undefined
  sidebarFolded?: boolean
  sandboxGuard?: SandboxSendGuard
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const { navigate } = useAdminView()
  const qc = useQueryClient()

  // /cancel infra: per-run cancel (X on the working card) + bulk
  // cancel (header button when at least one queued/running run exists).
  // Workspace id is read from useConversation so the hook is workspace-aware
  // without threading the id through every parent prop bag.
  const convInfoQ = useConversation(conversationId, null)
  const convWorkspaceId = convInfoQ.data?.workspace_id ?? null
  const cancelRunMut = useCancelRun(convWorkspaceId)
  const cancelConvMut = useCancelConversation()

  // SSE state: ComposerForm hands us a run_id after send; we open the
  // EventSource and append delta tokens into the streaming bubble. While
  // a stream is active we pause timeline polling so the half-written
  // assistant message doesn't get clobbered by a stale GET.
  const [activeRunId, setActiveRunId] = useState<string | null>(null)
  // Surface fire-and-forget /start failures (e.g. daemon offline,
  // network error). Server now auto-starts agent_daemon runs, so a
  // /start that returns 200 `already running` is fine; only true
  // network/5xx errors land here.
  const [chatToast, setChatToast] = useState<string | null>(null)
  const stream = useAgentRunStream(conversationId, activeRunId, { enabled: !!activeRunId })
  const hasActiveStream = !!activeRunId && stream.status !== "error" && stream.status !== "done"

  const timelineQ = useConversationTimeline(conversationId, undefined, {
    pollingEnabled: !hasActiveStream,
  })
  const messages = useMemo(
    () => dedupeCapabilityRuntimeDiagnostics(timelineQ.data?.messages ?? []),
    [timelineQ.data?.messages],
  )
  const runs = useMemo(() => timelineQ.data?.agent_runs ?? [], [timelineQ.data?.agent_runs])

  // Map output_message_id → runs[] so MessageRow can render StepTrace
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

  // We trust SSE status while a stream is active; otherwise fall back to
  // the run table (covers external runs or page-refresh-during-run cases).
  const someRunActive =
    hasActiveStream || runs.some((r) => r.status === "queued" || r.status === "running")

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
    const timer = window.setTimeout(() => setActiveRunId(null), 0)
    return () => window.clearTimeout(timer)
  }, [stream.status, activeRunId, conversationId, qc])

  return (
    <>
      <div
        className={cn(
          "border-b border-line/70 bg-surface/80 px-5 py-3 sm:px-6 lg:px-10",
          sidebarFolded && "pl-14 sm:pl-16 lg:pl-[72px]",
        )}
      >
        <div className="mx-auto flex max-w-4xl items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-xs font-medium uppercase text-fg-faint">
              {t("conversations.detail.kind")}
            </p>
            <h2 className="truncate text-base font-semibold text-fg">
              {agent?.name || t("conversations.sidebar.allAgentsHint")}
            </h2>
          </div>
          <div className="flex shrink-0 items-center gap-2 text-xs text-fg-subtle">
            <span
              className={cn(
                "rounded-md border px-2 py-1 font-medium",
                someRunActive
                  ? "border-success-border bg-success-subtle text-success"
                  : "border-line bg-surface text-fg-subtle",
              )}
            >
              {someRunActive ? t("conversations.stream.thinking") : t("conversations.stream.ready")}
            </span>
            {someRunActive && (
              <Button
                variant="outline"
                size="sm"
                disabled={cancelConvMut.isPending}
                onClick={() => {
                  // Drop activeRunId immediately so useAgentRunStream
                  // closes the EventSource without waiting for the
                  // server to send a done frame — daemon may take
                  // seconds to react to the abort, and the user
                  // expects the "thinking" + button to disappear at
                  // the moment of the click. The server still sees
                  // the EventSource close (it cancels the ctx and
                  // bails). The /stream re-subscription path that
                  // hits writeStreamHangError with status='cancelled'
                  // is handled by isUserCancelledError in
                  // api-conversations.ts — no banner shown.
                  setActiveRunId(null)
                  cancelConvMut.mutate({
                    conversationID: conversationId,
                    reason: "user_clicked_cancel_all",
                  })
                }}
                className="h-7 gap-1 px-2 text-xs text-danger hover:text-danger-emphasis"
                title={t("conversations.detail.cancelAllAria", {
                  defaultValue: "Cancel all in-flight tasks in this conversation",
                })}
              >
                {cancelConvMut.isPending ? (
                  <Loader2 className="h-3 w-3 animate-spin" strokeWidth={2.5} />
                ) : (
                  <X className="h-3 w-3" strokeWidth={2.5} />
                )}
                {t("conversations.detail.cancelAll", { defaultValue: "Cancel all" })}
              </Button>
            )}
          </div>
        </div>
      </div>

      <div className="flex flex-1 flex-col overflow-y-auto bg-surface-subtle">
        <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-5 px-5 py-6 sm:px-6 lg:px-10">
          {timelineQ.isLoading ? (
            <Skeleton className="h-24 w-3/4" />
          ) : messages.length === 0 ? (
            <p className="text-center text-sm text-fg-faint">
              {t("conversations.detail.emptyTimeline")}
            </p>
          ) : (
            messages.map((m) => (
              <MessageRow
                key={m.id}
                senderType={m.sender_type}
                messageType={m.kind}
                content={m.content}
                metadata={m.metadata}
                outputRuns={runsByOutputMessage.get(m.id)}
                stamp={fmtAgo(m.created_at)}
                agentName={agent?.name ?? ""}
                conversationId={conversationId}
                onOpenRun={(runID) => navigate("runs", { id: runID })}
              />
            ))
          )}
          {hasActiveStream && stream.deltaText && (
            <MessageRow
              senderType="agent"
              content={stream.deltaText}
              stamp={t("conversations.stream.caretHint")}
              agentName={agent?.name ?? ""}
              conversationId={conversationId}
            />
          )}
          <ConversationInteractionCards
            workspaceID={convWorkspaceId}
            conversationID={conversationId}
            preferredRequestID={stream.pendingInteraction?.requestId}
            onOpenInbox={() => navigate("approvals")}
          />
          {stream.status === "error" && (
            <div className="rounded-lg border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {t("conversations.stream.error", { error: stream.error ?? "" })}
            </div>
          )}
          {someRunActive &&
            !stream.deltaText &&
            (stream.steps.length > 0 ? (
              <WorkingSteps
                steps={stream.steps}
                cancelling={cancelRunMut.isPending}
                onCancel={
                  activeRunId
                    ? () => {
                        cancelRunMut.mutate({ runID: activeRunId, reason: "user_clicked_cancel" })
                      }
                    : undefined
                }
              />
            ) : (
              <div className="flex w-fit items-center gap-2 rounded-md bg-surface px-3 py-2 text-sm text-fg-subtle shadow-sm ring-1 ring-line/70">
                <span className="flex items-center gap-1" aria-hidden="true">
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success [animation-delay:-300ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success [animation-delay:-150ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-success" />
                </span>
                {hasActiveStream
                  ? t("conversations.stream.thinking")
                  : t("conversations.detail.agentTyping")}
              </div>
            ))}
          {/*
            Queued runs render an independent "queued" chip per run,
            distinct from the inflight working/thinking indicator
            above. Mirrors the Feishu queue-card driver behaviour
            (one chip per blocked message). Position is the timeline
            snapshot — same staleness budget as the surrounding
            5-second polling.
          */}
          {runs
            .filter((r) => r.status === "queued")
            .map((r) => (
              <div
                key={r.id}
                className="flex w-fit items-center gap-2 rounded-md border border-line/70 bg-surface-subtle px-3 py-2 text-sm text-fg-subtle shadow-sm"
              >
                <Clock className="h-3.5 w-3.5" strokeWidth={2.25} aria-hidden="true" />
                {r.queue_position && r.queue_position > 1
                  ? t("conversations.stream.queuedWithPosition", { position: r.queue_position })
                  : t("conversations.stream.queued")}
              </div>
            ))}
        </div>
      </div>

      <div className="border-t border-line/60 bg-surface/95 px-5 pb-4 pt-2 sm:px-6 lg:px-10">
        <div className="mx-auto max-w-4xl">
          {chatToast && <ChatErrorToast message={chatToast} onDismiss={() => setChatToast(null)} />}
          <ComposerForm
            conversationId={conversationId}
            placeholder={t("conversations.composer.placeholder", { agent: agent?.name ?? "" })}
            disabled={!agent || sandboxGuard?.blocked}
            onRunStarted={setActiveRunId}
            onStartError={setChatToast}
            activeRunId={activeRunId}
            // Drop activeRunId immediately on click for the same reason
            // the "Cancel all" header button does: stop showing "thinking" /
            // the in-progress affordance the moment the user asks for
            // it, instead of waiting for the daemon to acknowledge the
            // abort. Server-side useCancelRun handles the actual run
            // cancellation + connector.Abort.
            onCancelActiveRun={
              activeRunId
                ? () => {
                    const runID = activeRunId
                    setActiveRunId(null)
                    cancelRunMut.mutate({ runID, reason: "user_clicked_stop" })
                  }
                : undefined
            }
            cancelling={cancelRunMut.isPending}
            blockReason={sandboxGuard?.blocked ? sandboxGuard.message : undefined}
          />
        </div>
      </div>
    </>
  )
}

/**
 * Inline error banner above the chat composer. Ad-hoc on purpose
 * (mirrors capabilities/index.tsx:ToastBanner and AgentsPage local
 * banners): we only render one of these in one place today, and
 * extracting a shared component would force every callsite to agree
 * on dismiss / severity / icon semantics we don't actually share.
 */
function ChatErrorToast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  return (
    <div className="mb-2 flex items-start justify-between gap-3 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
      <span className="break-words">{message}</span>
      <button
        type="button"
        onClick={onDismiss}
        className="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium text-danger-emphasis hover:bg-danger-subtle"
      >
        ×
      </button>
    </div>
  )
}

function MessageRow({
  senderType,
  messageType,
  content,
  metadata,
  outputRuns,
  stamp,
  agentName,
  conversationId,
  onOpenRun,
}: {
  senderType: string
  messageType?: string
  content: string
  metadata?: Record<string, unknown>
  outputRuns?: ConversationTimelineRun[]
  stamp: string
  agentName: string
  conversationId: string
  onOpenRun?: (runID: string) => void
}) {
  const { i18n, t } = useTranslation("admin")
  const isUser = senderType === "user"
  if (isUser) {
    return (
      <div className="flex justify-end">
        <div className="flex max-w-[88%] flex-col items-end sm:max-w-[78%]">
          <div className="rounded-lg border border-success-border bg-success-subtle px-4 py-2.5 text-base leading-relaxed text-success-emphasis shadow-sm">
            <p className="whitespace-pre-wrap">{content}</p>
          </div>
          <div className="mt-1.5 text-right text-xs text-fg-faint">{stamp}</div>
        </div>
      </div>
    )
  }
  if (isRuntimeErrorMessage(messageType, metadata)) {
    const runtimeError = runtimeErrorViewModel(metadata, content, conversationId, i18n.language, t)
    const runtimeErrorSubKind =
      stringMeta(metadata, "sub_kind") || stringMeta(metadata, "payload.sub_kind")
    const runtimeErrorBadge = runtimeErrorSubKind.startsWith("capability_")
      ? t("conversations.runtime_error.capabilityBadge")
      : t("conversations.runtime_error.badge")
    return (
      <div className="flex">
        <div className="max-w-[78%]">
          <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-danger-emphasis">
            <AlertTriangle className="h-3.5 w-3.5" strokeWidth={2.25} />
            <span>{runtimeErrorBadge}</span>
          </div>
          <div className="rounded-xl border border-danger-border bg-danger-subtle px-3 py-2.5 text-base leading-relaxed text-danger-emphasis shadow-sm">
            <p className="font-medium">{runtimeError.message}</p>
            {runtimeError.href && runtimeError.action && (
              <a
                href={runtimeError.href}
                className="mt-2 inline-flex items-center rounded-md border border-danger-border bg-surface px-2.5 py-1 text-sm font-medium text-danger-emphasis transition-colors hover:bg-danger-subtle"
              >
                {runtimeError.action}
              </a>
            )}
            <p className="mt-2 text-sm text-danger-emphasis/80">{runtimeError.hint}</p>
          </div>
          <div className="mt-1.5 text-xs text-fg-faint">
            {agentName ? `${stamp} · ${agentName}` : stamp}
          </div>
        </div>
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
  return (
    <div className="flex">
      <div className="max-w-[82%] border-l border-line pl-4">
        <div className="mb-1.5 text-xs font-medium text-fg-faint">{agentName || "Agent"}</div>
        <div className="text-base leading-[1.7] text-fg">
          <p className="whitespace-pre-wrap">{content}</p>
        </div>
        {allSteps.length > 0 && <StepTrace steps={allSteps} />}
        {failedRun && onOpenRun && (
          <button
            type="button"
            onClick={() => onOpenRun(failedRun.id)}
            className="mt-1.5 text-sm text-fg-subtle hover:text-fg-muted hover:underline"
          >
            {t("conversations.detail.viewRunLink")} →
          </button>
        )}
        <div className="mt-1.5 text-xs text-fg-faint">{stamp}</div>
      </div>
    </div>
  )
}

function runtimeErrorViewModel(
  metadata: Record<string, unknown> | undefined,
  fallback: string,
  conversationId: string,
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
    t("capabilities.credentials.none"),
  )
  const current = `${window.location.pathname}${window.location.search || `?admin=conversations&id=${conversationId}`}`
  const href = credentialKind
    ? `?profile=credentials&kind=${encodeURIComponent(credentialKind)}&returnTo=${encodeURIComponent(current)}`
    : ""
  const manageCapabilityHref = capabilityID
    ? `?admin=capabilities&id=${encodeURIComponent(capabilityID)}`
    : "?admin=capabilities"

  switch (subKind) {
    case "capability_credential_missing":
      if (!credentialKind) {
        return {
          message: t("conversations.runtime_error.capability_unsupported", {
            name: capabilityName,
          }),
          action: t("conversations.runtime_error.manageCapability"),
          href: manageCapabilityHref,
          hint: t("conversations.runtime_error.unsupportedHint"),
        }
      }
      return {
        message: t("conversations.runtime_error.capability_credential_missing", {
          name: capabilityName,
          kind: kindLabel,
        }),
        action: t("conversations.runtime_error.addCredential"),
        href,
        hint: t("conversations.runtime_error.retryHint"),
      }
    case "capability_credential_decrypt_failed":
      return {
        message: t("conversations.runtime_error.capability_credential_decrypt_failed", {
          name: capabilityName,
        }),
        action: "",
        href: "",
        hint: t("conversations.runtime_error.retryHint"),
      }
    case "capability_credential_kind_mismatch":
      return {
        message: t("conversations.runtime_error.capability_credential_kind_mismatch", {
          name: capabilityName,
        }),
        action: t("conversations.runtime_error.resetCredential"),
        href,
        hint: t("conversations.runtime_error.retryHint"),
      }
    case "capability_unsupported":
      return {
        message: t("conversations.runtime_error.capability_unsupported", {
          name: capabilityName,
        }),
        action: t("conversations.runtime_error.manageCapability"),
        href: manageCapabilityHref,
        hint: t("conversations.runtime_error.unsupportedHint"),
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
        hint: t("conversations.runtime_error.versionUnavailableHint"),
      }
    default:
      return {
        message: fallback || t("conversations.runtime_error.generic"),
        action: "",
        href: "",
        hint: t("conversations.runtime_error.retryHint"),
      }
  }
}

/* ============================================================== */
/*  Composer — task input, Enter to send                            */
/* ============================================================== */

function ComposerForm({
  conversationId,
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
}: {
  conversationId: string
  placeholder: string
  disabled?: boolean
  autoFocus?: boolean
  /**
   * Optional override used by the empty-state composer. When set, the form
   * calls this instead of the conversationId-scoped send hook. Lets the
   * parent atomically createConversation + sendUserMessage + navigate.
   */
  onSendDirect?: (content: string) => Promise<void>
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
  const inputRef = useRef<HTMLInputElement | null>(null)
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

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    const text = trimmed
    if (onSendDirect) {
      setBusy(true)
      try {
        await onSendDirect(text)
        setContent("")
      } finally {
        setBusy(false)
      }
    } else {
      const resp = await sendMut.mutateAsync({ content: text })
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
    <form onSubmit={submit}>
      {blockReason && (
        <div className="mb-2 flex items-start gap-2 rounded-md border border-warning-border bg-warning-subtle px-3 py-2 text-sm text-warning-emphasis">
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" strokeWidth={2} aria-hidden="true" />
          <span className="break-words">{blockReason}</span>
        </div>
      )}
      <div
        className={cn(
          "flex min-h-[64px] items-center gap-3 rounded-lg border border-line bg-surface px-3 py-2 shadow-[0_1px_2px_rgba(15,23,42,0.04),_0_12px_34px_rgba(15,23,42,0.08)] transition-shadow",
          "hover:shadow-[0_1px_2px_rgba(15,23,42,0.06),_0_16px_40px_rgba(15,23,42,0.10)]",
          disabled && "opacity-60",
        )}
      >
        <Input
          ref={inputRef}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder={placeholder}
          disabled={disabled || (!conversationId && !onSendDirect)}
          className="flex-1 border-0 bg-transparent px-2 text-base shadow-none focus-visible:ring-0"
        />
        {showStop ? (
          <button
            type="button"
            onClick={onCancelActiveRun}
            disabled={cancelling}
            aria-label={t("conversations.composer.stopAria", { defaultValue: "Stop" })}
            title={t("conversations.composer.stopAria", { defaultValue: "Stop" })}
            className={cn(
              "flex h-10 w-10 shrink-0 items-center justify-center rounded-md transition-colors",
              "bg-surface-inverse text-white hover:bg-surface-emphasis",
              cancelling && "opacity-60",
            )}
          >
            {cancelling ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Square className="h-3.5 w-3.5 fill-current" />
            )}
          </button>
        ) : (
          <button
            type="submit"
            disabled={!canSubmit}
            className={cn(
              "flex h-10 w-10 shrink-0 items-center justify-center rounded-md transition-colors",
              canSubmit
                ? "bg-surface-inverse text-white hover:bg-surface-emphasis"
                : "bg-surface-muted text-fg-faint",
            )}
            aria-label="send"
          >
            {isBusy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
          </button>
        )}
      </div>
    </form>
  )
}

/* ============================================================== */
/*  Utilities                                                        */
/* ============================================================== */

function truncate(s: string, n: number): string {
  if (!s) return ""
  if (s.length <= n) return s
  return s.slice(0, n) + "…"
}
