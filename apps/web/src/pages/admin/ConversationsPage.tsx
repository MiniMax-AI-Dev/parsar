import { useQueryClient } from "@tanstack/react-query"
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  AlertTriangle,
  Check,
  ChevronsUpDown,
  Loader2,
  MessageSquarePlus,
  PanelLeftClose,
  Link2,
  Pencil,
  Trash2,
  X,
} from "lucide-react"

import { ConversationMain, tailId } from "../../components/conversation/ConversationThread"
import { AdminLayout } from "../../components/layout/AdminLayout"
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
import { Input } from "../../components/ui/input"
import { InitialTile, Ledger, LedgerId, LedgerRow, col } from "../../components/ui/ledger"
import { Skeleton } from "../../components/ui/skeleton"
import { copyText } from "../../lib/clipboard"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAgents } from "../../lib/api-agents"
import { agentNeedsSandbox } from "../../lib/agent-runtime"
import {
  createConversation,
  sendUserMessage,
  useConversation,
  useDeleteConversation,
  useConversations,
  useUpdateConversationTitle
} from "../../lib/api-conversations"
import { useSandboxBinding } from "../../lib/api-sandbox"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import type {
  ConversationListItem,
  Agent,
} from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"
import {
  forgetConversationViewConversation,
  readConversationViewState,
  writeConversationViewState,
} from "../../lib/conversation-view-state"

const FOLD_KEY = "parsar:conv:sidebarFolded"

/** Reading measure of the thread column; the components below read it. */

/** title · conversation id · age (actions replace the age on hover) */
const LIST_COLUMNS = [col.title(120, 2), col.id(64, 0.3), col.age(56, 0.3), col.actions(2)]

import { sandboxSendGuard } from "../../lib/sandbox-send-guard"

/* ============================================================== */
/*  ConversationsPage — the list; the thread is a shared surface   */
/* ============================================================== */

export function ConversationsPage() {
  const { t } = useTranslation("admin")
  const { entityId, navigate } = useAdminView()
  const focusTarget = new URLSearchParams(window.location.search).get("focus")
  const wsId = useWorkspaceId()
  const restoreViewState = !entityId && focusTarget !== "compose"
  const savedViewState = useMemo(() => readConversationViewState(wsId), [wsId])
  const workspacesQ = useMyWorkspaces()
  const workspaces = workspacesQ.data?.workspaces ?? []
  const writableWorkspaceIds = new Set(
    workspaces
      .filter((w) => w.role === "owner" || w.role === "admin" || w.role === "member")
      .map((w) => w.id),
  )
  const workspaceRole = workspaces.find((w) => w.id === wsId)?.role
  const canWrite = writableWorkspaceIds.has(wsId ?? "")

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
  const firstSend = useRef<{
    workspaceId: string
    agentId: string
    conversationId?: string
  } | null>(null)

  useEffect(() => {
    firstSend.current = null
  }, [wsId, selectedAgentId, entityId])

  // "New conversation" navigates to an empty composer without pre-creating a
  // conv — the conv is created on first send via handleSendFromEmpty,
  // so the list only shows rows with a real first user turn.
  const openCreate = () => {
    firstSend.current = null
    navigate("conversations", { id: "", focus: "compose" })
  }

  // First-send creates the conv + posts the message + navigates in.
  // Title derives from the first 30 chars so the list gets a
  // meaningful name immediately (server defaults to "Untitled conversation").
  const handleSendFromEmpty = async (content: string): Promise<boolean> => {
    if (!wsId || !selectedAgentId) {
      throw new Error("workspace_id and agent_id required for empty-state send")
    }
    let attempt = firstSend.current
    if (!attempt || attempt.workspaceId !== wsId || attempt.agentId !== selectedAgentId) {
      attempt = { workspaceId: wsId, agentId: selectedAgentId }
      firstSend.current = attempt
    }
    let cid = attempt.conversationId
    if (!cid) {
      const conv = await createConversation(wsId, {
        title: content.slice(0, 30),
        surface: "web",
        form: "thread",
        agent_id: selectedAgentId,
      })
      cid = conv.id
      const createdAttempt = { ...attempt, conversationId: cid }
      if (firstSend.current === attempt) firstSend.current = createdAttempt
      attempt = createdAttempt
    }
    try {
      await sendUserMessage(cid, { content })
    } finally {
      // A failed first message still leaves a real conversation in the list.
      qc.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === "admin" && q.queryKey[1] === "conversations" && q.queryKey[2] === wsId,
      })
      qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", cid] })
    }
    if (firstSend.current !== attempt) return false
    firstSend.current = null
    writeConversationViewState(wsId, {
      agentId: selectedAgentId,
      conversationId: cid,
    })
    navigate("conversations", { id: cid })
    return true
  }

  const renameMutation = useUpdateConversationTitle(wsId)
  const deleteMutation = useDeleteConversation(wsId)
  const handleRenameConversation = async (cid: string, title: string): Promise<void> => {
    await renameMutation.mutateAsync({ cid, title })
  }
  const handleDeleteConversation = async (cid: string): Promise<void> => {
    // Clear the selection BEFORE the mutation: deleting invalidates the
    // conversation queries, and a still-mounted detail would refetch the
    // row we are deleting and 404. If the delete then fails the dialog
    // reports it and the conversation is still in the list.
    if (cid === entityId) {
      navigate("conversations", { id: "", focus: "compose" })
    }
    await deleteMutation.mutateAsync(cid)
    forgetConversationViewConversation(wsId, cid)
  }

  return (
    <AdminLayout activeMenu="conversations" fullBleed>
      <div className="flex min-h-0 flex-1">
        {!folded && (
          <ConversationList
            agents={allAgents}
            canWrite={canWrite}
            canDelete={workspaceRole === "owner" || workspaceRole === "admin"}
            selectedAgentId={selectedAgentId}
            onPickAgent={(id) => {
              firstSend.current = null
              setPickedAgent({ workspaceId: wsId, agentId: id })
              writeConversationViewState(wsId, { agentId: id })
              navigate("conversations", { id: "", focus: "compose" })
            }}
            agentsLoading={agentsQ.isLoading}
            conversations={conversations}
            selectedConversationId={entityId ?? ""}
            onPickConversation={(id) => {
              firstSend.current = null
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
          canWrite={entityId ? writableWorkspaceIds.has(currentConv?.workspace_id ?? "") : canWrite}
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


/* ============================================================== */
/*  Conversation list (panel tone, ledger rows)                    */
/* ============================================================== */

interface ListProps {
  agents: Agent[]
  canWrite: boolean
  canDelete: boolean
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
  const [copiedId, setCopiedId] = useState<string | null>(null)
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
  if (!p.canWrite && renamingConvId !== null) setRenamingConvId(null)
  if (!p.canDelete && deleteConvId !== null) setDeleteConvId(null)
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
    if (!p.canWrite || !renamingConvId) return
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
    if (!p.canDelete || !deleteConvId) return
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
              className="app-shadow-floating z-50 min-w-[260px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
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
        <EmptyState size="compact" title={t("conversations.sidebar.emptyForAgent")} />
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
                  className={cn(isRenaming && "h-auto min-h-9 py-1")}
                >
                  {isRenaming ? (
                    <div className="col-span-4 flex flex-col gap-1" onClick={(e) => e.stopPropagation()}>
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
                        <RowActions inline>
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
                      <span className="truncate text-right text-xs text-fg-muted">
                        {fmtAgo(c.last_message_at ?? c.updated_at)}
                      </span>
                      {/* Copying a link needs no write access — a viewer can
                          share what they can already read. Rename and delete
                          keep their own guards (main #283). */}
                      <RowActions>
                        <ActionIconButton
                          icon={Link2}
                          label={copiedId === c.id ? t("conversations.sidebar.copied") : t("conversations.sidebar.copyLinkAria")}
                          onClick={() => {
                            void copyText(`${window.location.origin}/c/${c.id}`).then((ok) => {
                              setCopiedId(ok ? c.id : null)
                              if (ok) window.setTimeout(() => setCopiedId(null), 2000)
                            })
                          }}
                        />
                        {p.canWrite && (
                        <ActionIconButton
                          icon={Pencil}
                          label={t("conversations.sidebar.renameAria")}
                          onClick={() => startRename(c)}
                        />
                        )}
                        {p.canDelete && (
                        <ActionIconButton
                          icon={Trash2}
                          tone="danger"
                          label={t("conversations.sidebar.deleteAria")}
                          onClick={() => {
                            setDeleteError("")
                            setDeleteConvId(c.id)
                          }}
                        />
                        )}
                      </RowActions>
                    </>
                  )}
                </LedgerRow>
              )
            })}
          </ul>
        </Ledger>
      )}

      <Dialog
        open={p.canDelete && deleteConvId !== null}
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
