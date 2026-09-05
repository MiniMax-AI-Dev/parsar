import { useQueryClient } from "@tanstack/react-query"
import { memo, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  AlertTriangle,
  ArrowUpRight,
  Check,
  ChevronsUpDown,
  Loader2,
  MessageSquare,
  MessageSquarePlus,
  PanelLeftClose,
  PanelLeftOpen,
  Pencil,
  Send,
  Square,
  Trash2,
  X,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ConversationInteractionCards } from "../../components/conversation/ConversationInteractionCards"
import { WorkingSteps, StepTrace } from "../../components/conversation/StepDisplay"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { InitialTile, Ledger, LedgerId, LedgerRow } from "../../components/ui/ledger"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon } from "../../components/ui/status-icon"
import { Textarea } from "../../components/ui/textarea"
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
import { ToolCardSlot, SingleSlot, ListSlot } from "../../components/plugin/SlotRenderer"

const FOLD_KEY = "parsar:conv:sidebarFolded"

/** Reading measure of the thread column; the components below read it. */
const THREAD_STYLE = { ["--thread-max-width" as string]: "48rem" }

/** title · conversation id · age (actions replace the age on hover) */
const LIST_COLUMNS = "minmax(0,1fr) 64px 60px"

interface SandboxSendGuard {
  blocked: boolean
  message: string
}

/* ============================================================== */
/*  ConversationsPage — shell: conversation list | thread          */
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

  // List selection follows the active conv's primary_agent_id; when
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

  // List conversations: scoped to the selected agent.
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
  // so the list only shows rows with a real first user turn.
  const openCreate = () => {
    navigate("conversations", { id: "", focus: "compose" })
  }

  // First-send creates the conv + posts the message + navigates in.
  // Title derives from the first 30 chars so the list gets a
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
      <div className="flex min-h-0 flex-1">
        {!folded && (
          <ConversationList
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
/*  Conversation list (panel tone, ledger rows)                    */
/* ============================================================== */

interface ListProps {
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

function ConversationList(p: ListProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const fmtAgo = useRelativeTime()
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

  const agentLabel = selectedAgent?.name || t("conversations.sidebar.allAgentsHint")
  const listLabel = tc("nav.items.conversations")

  return (
    <aside className="flex w-[300px] shrink-0 flex-col border-r border-line bg-surface-subtle">
      {/* 64px header (matches the shell topbar): agent switcher · new conversation · fold */}
      <div className="flex h-16 shrink-0 items-center gap-1 border-b border-line pl-2 pr-2">
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button
              type="button"
              aria-label={t("conversations.sidebar.switchAgent")}
              className="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-sm hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 data-[state=open]:app-pressed"
            >
              {selectedAgent && <InitialTile name={selectedAgent.name} />}
              <span className={cn("min-w-0 flex-1 truncate", selectedAgent ? "font-medium text-fg" : "text-fg-muted")}>
                {agentLabel}
              </span>
              <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="start"
              sideOffset={6}
              className="app-shadow-floating z-50 min-w-[260px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"
            >
              {p.agentsLoading ? (
                <div className="space-y-2 p-2">
                  <Skeleton className="h-3 w-3/4" />
                  <Skeleton className="h-3 w-1/2" />
                </div>
              ) : p.agents.length === 0 ? (
                <p className="m-0 px-2 py-1.5 text-sm text-fg-muted">{t("conversations.sidebar.allAgentsHint")}</p>
              ) : (
                <DropdownMenu.RadioGroup value={p.selectedAgentId} onValueChange={p.onPickAgent}>
                  {p.agents.map((a) => (
                    <DropdownMenu.RadioItem
                      key={a.id}
                      value={a.id}
                      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
                    >
                      <InitialTile name={a.name} />
                      <span className="min-w-0 flex-1 truncate">{a.name}</span>
                      <DropdownMenu.ItemIndicator>
                        <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                      </DropdownMenu.ItemIndicator>
                    </DropdownMenu.RadioItem>
                  ))}
                </DropdownMenu.RadioGroup>
              )}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>

        <Button
          variant="outline"
          size="icon"
          onClick={p.onNewConversation}
          disabled={!p.selectedAgentId}
          aria-label={t("conversations.sidebar.newConversation")}
          title={t("conversations.sidebar.newConversation")}
        >
          <MessageSquarePlus strokeWidth={1.5} aria-hidden="true" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={p.onFold}
          aria-label={t("conversations.sidebar.foldAria")}
          title={t("conversations.sidebar.foldAria")}
        >
          <PanelLeftClose strokeWidth={1.5} aria-hidden="true" />
        </Button>
      </div>

      {p.convsLoading ? (
        <div className="px-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
              <Skeleton className="h-3 flex-1" />
              <Skeleton className="h-3 w-12" />
            </div>
          ))}
        </div>
      ) : p.conversations.length === 0 ? (
        <p className="m-0 px-4 py-6 text-center text-sm text-fg-muted">
          {t("conversations.sidebar.emptyForAgent")}
        </p>
      ) : (
        <Ledger columns={LIST_COLUMNS} role="listbox" aria-label={listLabel}>
          <ul className="m-0 list-none p-0">
            {p.conversations.map((c) => {
              const isActive = c.id === p.selectedConversationId
              const isRenaming = renamingConvId === c.id
              const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
                if (isRenaming) return
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault()
                  p.onPickConversation(c.id)
                }
              }
              return (
                <LedgerRow
                  key={c.id}
                  selected={isActive}
                  tabIndex={isRenaming ? -1 : 0}
                  onClick={() => {
                    if (!isRenaming) p.onPickConversation(c.id)
                  }}
                  onKeyDown={onKeyDown}
                  className={cn("group/row", isRenaming && "h-auto min-h-9 py-1")}
                >
                  {isRenaming ? (
                    <div className="col-span-3 flex flex-col gap-1" onClick={(e) => e.stopPropagation()}>
                      <div className="flex items-center gap-1">
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
                          disabled={renameBusy}
                          aria-label={t("conversations.sidebar.renameAria")}
                        />
                        <RowActions>
                          <ActionIconButton
                            icon={X}
                            label={t("conversations.sidebar.renameCancel")}
                            onClick={cancelRename}
                            disabled={renameBusy}
                          />
                          <ActionIconButton
                            icon={Check}
                            label={t("conversations.sidebar.renameCommit")}
                            onClick={() => void commitRename()}
                            busy={renameBusy}
                          />
                        </RowActions>
                      </div>
                      {renameError && (
                        <p className="m-0 flex items-start gap-1.5 text-xs text-fg">
                          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                          <span>{renameError}</span>
                        </p>
                      )}
                    </div>
                  ) : (
                    <>
                      <span className="truncate font-medium" title={c.title || undefined}>
                        {c.title || t("conversations.detail.unnamed")}
                      </span>
                      <LedgerId>{tailId(c.id)}</LedgerId>
                      <span className="relative flex h-full items-center justify-end">
                        <span className="truncate text-xs text-fg-muted group-focus-within/row:invisible group-hover/row:invisible">
                          {fmtAgo(c.last_message_at ?? c.updated_at)}
                        </span>
                        <RowActions className="absolute inset-y-0 right-0 hidden group-focus-within/row:flex group-hover/row:flex">
                          <ActionIconButton
                            icon={Pencil}
                            label={t("conversations.sidebar.renameAria")}
                            onClick={() => startRename(c)}
                          />
                          <ActionIconButton
                            icon={Trash2}
                            tone="danger"
                            label={t("conversations.sidebar.deleteAria")}
                            onClick={() => {
                              setDeleteError("")
                              setDeleteConvId(c.id)
                            }}
                          />
                        </RowActions>
                      </span>
                    </>
                  )}
                </LedgerRow>
              )
            })}
          </ul>
        </Ledger>
      )}

      <Dialog
        open={deleteConvId !== null}
        onOpenChange={(next) => {
          if (!next && !deleteBusy) setDeleteConvId(null)
        }}
      >
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{t("conversations.sidebar.deleteConfirmTitle")}</DialogTitle>
            <DialogDescription>
              {t("conversations.sidebar.deleteConfirmDesc", {
                title: deleteConv?.title || t("conversations.detail.unnamed"),
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            {deleteError && (
              <p className="m-0 mr-auto flex items-start gap-1.5 text-sm text-fg">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                <span>{deleteError}</span>
              </p>
            )}
            <Button variant="outline" onClick={() => setDeleteConvId(null)} disabled={deleteBusy}>
              {t("conversations.sidebar.deleteCancel")}
            </Button>
            <Button variant="destructive" onClick={() => void confirmDelete()} disabled={deleteBusy}>
              {deleteBusy && <Loader2 className="animate-spin" aria-hidden="true" />}
              {t("conversations.sidebar.deleteConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </aside>
  )
}

/* ============================================================== */
/*  Main column — header + body (empty or stream) + composer        */
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
  /** Empty-state send: create conv + post first message + navigate. */
  onSendFromEmpty: (content: string) => Promise<void>
  onRenameAfterFirstMessage: (cid: string, title: string) => Promise<void>
  focusComposer?: boolean
  sandboxGuard?: SandboxSendGuard
}

function ConversationMain(p: MainProps) {
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
        <ThreadHeader folded={p.folded} onExpand={p.onExpand} />
        <div className="px-6 pt-6">
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
      </div>
    )
  }

  if (p.convLoading) {
    return (
      <div className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
        <ThreadHeader folded={p.folded} onExpand={p.onExpand} />
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
        folded={p.folded}
        onExpand={p.onExpand}
        onSendFromEmpty={p.onSendFromEmpty}
        focusComposer={p.focusComposer}
        sandboxGuard={p.sandboxGuard}
      />
    )
  }

  if (p.messageCount === 0) {
    // 0 messages: same empty surface as the no-conversation state until
    // the first send; interaction cards still show for this conv.
    return (
      <EmptyChat
        agent={p.agent}
        folded={p.folded}
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
      agent={p.agent}
      folded={p.folded}
      onExpand={p.onExpand}
      sandboxGuard={p.sandboxGuard}
    />
  )
}

/** Hairline-topped footer that holds the composer at the thread's measure. */
function ComposerFooter({ children }: { children: React.ReactNode }) {
  return (
    <div className="shrink-0 border-t border-line px-4 py-3">
      <div className="mx-auto w-full max-w-[var(--thread-max-width)]">{children}</div>
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
  conversationId,
  workspaceID,
  onSendFromEmpty,
  onRenameAfterFirstMessage,
  focusComposer,
  sandboxGuard,
}: {
  agent: Agent | undefined
  folded: boolean
  onExpand: () => void
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
  return (
    <div className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
      <ThreadHeader
        folded={folded}
        onExpand={onExpand}
        actions={
          conversationId ? (
            <ListSlot slotId="conversation.header.actions" context={{ conversationId, agent }} />
          ) : undefined
        }
      />
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
      </ComposerFooter>
    </div>
  )
}

/* ============================================================== */
/*  Chat stream — user (muted block, right) + agent (ink, left)     */
/* ============================================================== */

function ChatStream({
  conversationId,
  agent,
  folded,
  onExpand,
  sandboxGuard,
}: {
  conversationId: string
  agent: Agent | undefined
  folded: boolean
  onExpand: () => void
  sandboxGuard?: SandboxSendGuard
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const { navigate } = useAdminView()
  const qc = useQueryClient()
  const openRun = useCallback((runID: string) => navigate("runs", { id: runID }), [navigate])

  // /cancel infra: per-run cancel (X on the working steps) + bulk
  // cancel (header button when at least one queued/running run exists).
  // Workspace id is read from useConversation so the hook is workspace-aware
  // without threading the id through every parent prop bag.
  const convInfoQ = useConversation(conversationId, null)
  const convWorkspaceId = convInfoQ.data?.workspace_id ?? null
  const cancelRunMut = useCancelRun(convWorkspaceId)
  const cancelConvMut = useCancelConversation()

  // SSE state: ComposerForm hands us a run_id after send; we open the
  // EventSource and append delta tokens into the streaming message. While
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
  const streamErrorMessage = stream.error
    ? isContextRejectedStreamError(stream.error)
      ? t("conversations.stream.contextRejected")
      : t("conversations.stream.error", { error: stream.error })
    : null

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
    if (activeRunId) return
    const runningRun = runs.find((r) => r.status === "running")
    if (!runningRun) return
    const timer = window.setTimeout(() => setActiveRunId(runningRun.id), 0)
    return () => window.clearTimeout(timer)
  }, [activeRunId, runs])

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
    const timer = window.setTimeout(() => {
      if (stream.status === "error" && streamErrorMessage) {
        // activeRunId is cleared below, which resets the stream hook to idle.
        // Keep a durable, dismissible copy so a fast provider rejection does
        // not disappear before the user can read it and look like a stalled run.
        setChatToast(streamErrorMessage)
      }
      setActiveRunId(null)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [stream.status, streamErrorMessage, activeRunId, conversationId, qc])

  const cancelAllLabel = t("conversations.detail.cancelAll")

  return (
    <div className="flex min-w-0 flex-1 flex-col" style={THREAD_STYLE}>
      <ThreadHeader
        folded={folded}
        onExpand={onExpand}
        actions={
          <>
            {someRunActive && (
              <>
                <StatusIcon status="running" title={t("conversations.stream.thinking")} />
                <Button
                  variant="outline"
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
                  title={t("conversations.detail.cancelAllAria")}
                >
                  {cancelConvMut.isPending ? (
                    <Loader2 className="animate-spin" aria-hidden="true" />
                  ) : (
                    <X strokeWidth={1.5} aria-hidden="true" />
                  )}
                  {cancelAllLabel}
                </Button>
              </>
            )}
            <ListSlot slotId="conversation.header.actions" context={{ conversationId, agent }} />
          </>
        }
      />

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[var(--thread-max-width)] flex-1 flex-col gap-5 px-4 py-6">
          {timelineQ.isLoading ? (
            <Skeleton className="h-16 w-3/4" />
          ) : messages.length === 0 ? (
            <p className="m-0 py-6 text-center text-sm text-fg-muted">
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
                onOpenRun={openRun}
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
          />
          {stream.status === "error" && (
            <p className="m-0 flex items-start gap-1.5 text-sm text-fg">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
              <span>{streamErrorMessage}</span>
            </p>
          )}
          {someRunActive && stream.steps.length === 0 && (
            <p className="m-0 flex items-center gap-2 text-sm text-fg" role="status" aria-live="polite">
              <StatusIcon status="running" />
              <span>{t("conversations.stream.thinking")}</span>
            </p>
          )}
          {someRunActive && stream.steps.length > 0 && (
            <WorkingSteps
              steps={stream.steps}
              active={someRunActive}
              cancelling={cancelRunMut.isPending}
              onCancel={
                activeRunId
                  ? () => {
                      cancelRunMut.mutate({ runID: activeRunId, reason: "user_clicked_cancel" })
                    }
                  : undefined
              }
            />
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

      <ComposerFooter>
        <ListSlot slotId="conversation.input.dock" context={{ conversationId }} />
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
      </ComposerFooter>
    </div>
  )
}

/**
 * Inline error line above the chat composer: a failed-red triangle, the
 * message in ink, one ghost dismiss button. Ad-hoc on purpose — it is
 * rendered in one place today.
 */
function ChatErrorToast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  const { t: tc } = useTranslation("common")
  return (
    <div className="mb-2 flex items-start gap-1.5 text-sm text-fg">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
      <span className="min-w-0 flex-1 break-words">{message}</span>
      <Button variant="ghost" size="icon" className="-my-1.5 h-6 w-6" onClick={onDismiss} aria-label={tc("actions.close")}>
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
        <div className="flex max-w-[85%] flex-col items-end">
          <div className="rounded-md bg-surface-muted px-3 py-2 text-base text-fg">
            <p className="m-0 whitespace-pre-wrap break-words">{content}</p>
          </div>
          <div className="mt-1 text-xs text-fg-muted">{stamp}</div>
        </div>
      </div>
    )
  }
  const byline = agentName ? `${agentName} · ${stamp}` : stamp
  if (messageType === "runtime_error") {
    const runtimeError = runtimeErrorViewModel(metadata, content, conversationId, i18n.language, t)
    return (
      <div className="max-w-[85%]">
        <div className="mb-1 text-xs text-fg-muted">{byline}</div>
        <div className="flex items-start gap-1.5 text-base text-fg">
          <AlertTriangle className="mt-1 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <div className="min-w-0">
            <p className="m-0 font-medium">{t("conversations.runtime_error.badge")}</p>
            <p className="m-0 mt-1 break-words">{runtimeError.message}</p>
            {runtimeError.href && runtimeError.action && (
              <Button asChild variant="outline" size="sm" className="mt-2">
                <a href={runtimeError.href}>{runtimeError.action}</a>
              </Button>
            )}
            <p className="m-0 mt-2 text-xs text-fg-muted">{t("conversations.runtime_error.retryHint")}</p>
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
        fallback={<p className="m-0 whitespace-pre-wrap break-words text-base text-fg">{content}</p>}
      />
      {allSteps.length > 0 && <StepTrace steps={allSteps} />}
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
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      {blockReason && (
        <p className="m-0 flex items-start gap-1.5 text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span className="break-words">{blockReason}</span>
        </p>
      )}
      <Textarea
        ref={inputRef}
        rows={2}
        value={content}
        onChange={(e) => setContent(e.target.value)}
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
      />
      <div className="flex items-center justify-end gap-2">
        {showStop ? (
          <Button type="button" variant="outline" onClick={onCancelActiveRun} disabled={cancelling}>
            {cancelling ? (
              <Loader2 className="animate-spin" aria-hidden="true" />
            ) : (
              <Square className="fill-current" strokeWidth={1.5} aria-hidden="true" />
            )}
            {t("conversations.composer.stopAria")}
          </Button>
        ) : (
          <Button type="submit" disabled={!canSubmit}>
            {isBusy ? (
              <Loader2 className="animate-spin" aria-hidden="true" />
            ) : (
              <Send strokeWidth={1.5} aria-hidden="true" />
            )}
            {isBusy ? t("conversations.composer.sending") : t("conversations.composer.send")}
          </Button>
        )}
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
function tailId(s?: string, n = 8): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(-n)
}
