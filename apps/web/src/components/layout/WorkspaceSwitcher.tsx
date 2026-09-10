import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  ChevronsUpDown,
  Clock,
  Globe,
  Layers,
  Plus,
  RefreshCw,
  Send,
  X,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { BrandMark } from "../ui/brand-mark"
import { InlineNotice } from "../ui/error-state"
import { WorkspaceMenuItem } from "./WorkspaceMenuItem"
import {
  setWorkspaceId,
  useWorkspaceId,
} from "../../lib/workspace"
import {
  useArchiveWorkspace,
  useCreateWorkspace,
  useDiscoverableWorkspaces,
  useMyWorkspaces,
  useRequestJoinWorkspace,
  useUpdateWorkspace,
  useWithdrawJoinRequest,
} from "../../lib/api-workspaces"
import type {
  DiscoverableWorkspace,
  UserWorkspace,
} from "../../lib/api-types"
import {
  ConfirmArchiveDialog,
  JoinRequestDialog,
  WorkspaceFormDialog,
} from "./WorkspaceCrudDialogs"
import { DiscoverWorkspacesDialog } from "./DiscoverWorkspacesDialog"
import { useWorkspaceDialogFocus } from "./useWorkspaceDialogFocus"

function shortId(id: string | null | undefined): string {
  if (!id) return ""
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

export function WorkspaceSwitcher() {
  const { t } = useTranslation("common")
  const wsId = useWorkspaceId()
  const workspacesQuery = useMyWorkspaces()
  // Cap at 5; overflow opens the full DiscoverWorkspacesDialog so the
  // dropdown can't be blown up by hundreds of workspaces.
  const discoverableQuery = useDiscoverableWorkspaces({ limit: 5 })
  const [discoverDialogOpen, setDiscoverDialogOpen] = useState(false)

  const workspaces = useMemo(
    () => workspacesQuery.data?.workspaces ?? [],
    [workspacesQuery.data?.workspaces]
  )
  const discoverable = useMemo(
    () => discoverableQuery.data?.workspaces ?? [],
    [discoverableQuery.data?.workspaces]
  )
  const discoverableTotal = discoverableQuery.data?.total ?? discoverable.length
  const currentWorkspace = workspaces.find((w) => w.id === wsId)

  // Self-heal: stale wsId from localStorage (archived ws or old dev seed)
  // would leave currentWorkspace undefined; auto-pick the first one.
  useEffect(() => {
    if (workspacesQuery.isLoading || workspacesQuery.isFetching || workspacesQuery.isError || workspaces.length === 0) return
    if (wsId && workspaces.some((w) => w.id === wsId)) return
    setWorkspaceId(workspaces[0].id)
  }, [wsId, workspaces, workspacesQuery.isLoading, workspacesQuery.isFetching, workspacesQuery.isError])

  // Dialog state lives here so Radix Dropdown's focus trap doesn't fight
  // the dialog's focus trap.
  const [createWsOpen, setCreateWsOpen] = useState(false)
  const [renameWs, setRenameWs] = useState<UserWorkspace | null>(null)
  const [archiveWs, setArchiveWs] = useState<UserWorkspace | null>(null)
  const [joinTarget, setJoinTarget] = useState<DiscoverableWorkspace | null>(
    null
  )
  const dialogFocus = useWorkspaceDialogFocus(
    createWsOpen || !!renameWs || !!archiveWs || !!joinTarget || discoverDialogOpen
  )
  // Toast rendered next to the trigger (not page-top) since the switcher
  // is inside a dropdown. Auto-dismiss after 3s.
  const [joinToast, setJoinToast] = useState<string | null>(null)
  useEffect(() => {
    if (!joinToast) return
    const id = window.setTimeout(() => setJoinToast(null), 3000)
    return () => window.clearTimeout(id)
  }, [joinToast])

  const createWorkspaceMut = useCreateWorkspace()
  const updateWorkspaceMut = useUpdateWorkspace()
  const archiveWorkspaceMut = useArchiveWorkspace()
  const requestJoinMut = useRequestJoinWorkspace()
  const withdrawJoinMut = useWithdrawJoinRequest()

  const triggerLabel = currentWorkspace?.name
    ? currentWorkspace.name
    : wsId
      ? `WS · ${shortId(wsId)}`
      : t("workspaceSwitcher.workspaceLabel")

  return (
    <>
      <DropdownMenu.Root
        onOpenChange={(open) => {
          if (open) void workspacesQuery.refetch({ cancelRefetch: false })
        }}
      >
        <DropdownMenu.Trigger asChild>
          <button
            ref={dialogFocus.triggerRef}
            type="button"
            aria-label={t("workspaceSwitcher.triggerAriaLabel")}
            title={triggerLabel}
            className="flex h-8 w-full items-center gap-1.5 rounded-md px-2 text-left text-sm hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 data-[state=open]:app-pressed"
          >
            <BrandMark size={14} />
            <span className="shrink-0 font-medium text-fg" translate="no">Parsar</span>
            <span className="shrink-0 text-fg-muted" aria-hidden="true">/</span>
            <span className="min-w-0 flex-1 truncate text-fg-muted">{triggerLabel}</span>
            {!wsId && (
              <span
                title={t("workspace.mockTooltip")}
                className="rounded bg-warning-subtle px-1 py-0 text-xs font-medium text-warning"
              >
                {t("workspace.mockBadge")}
              </span>
            )}
            <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} />
          </button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            ref={dialogFocus.menuRef}
            onFocusCapture={dialogFocus.rememberFocus}
            align="start"
            sideOffset={6}
            className="app-shadow-floating z-50 max-h-[var(--radix-dropdown-menu-content-available-height)] w-96 max-w-[calc(100vw-1rem)] overflow-x-hidden overflow-y-auto rounded-lg border border-line bg-surface p-1 text-sm text-fg-muted animate-pop-in data-[state=closed]:animate-pop-out"
          >
            <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-xs font-medium text-fg-subtle">
              <Layers className="h-3 w-3" strokeWidth={1.75} />
              {t("workspaceSwitcher.workspaceLabel")}
            </DropdownMenu.Label>

            {workspacesQuery.isError && (
              <>
                <InlineNotice tone="error" className="px-2 py-2">
                  {t(workspaces.length > 0 ? "workspaceSwitcher.refreshFailed" : "workspaceSwitcher.loadFailed")}
                </InlineNotice>
                <DropdownMenu.Item
                  disabled={workspacesQuery.fetchStatus !== "idle"}
                  onSelect={(event) => {
                    event.preventDefault()
                    void workspacesQuery.refetch()
                  }}
                  className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-fg outline-none data-[highlighted]:bg-surface-muted data-[disabled]:opacity-50"
                >
                  <RefreshCw className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
                  {workspacesQuery.fetchStatus !== "idle" ? t("states.loading") : t("actions.retry")}
                </DropdownMenu.Item>
              </>
            )}

            {!workspacesQuery.isError && workspaces.length === 0 && (
              <div className="px-2 py-2 text-sm text-fg-faint">
                {workspacesQuery.isLoading
                  ? t("states.loading")
                  : t("workspaceSwitcher.noWorkspaces")}
              </div>
            )}

            {workspaces.map((ws) => (
              <WorkspaceMenuItem
                key={ws.id}
                workspace={ws}
                active={ws.id === wsId}
                onRename={() => setRenameWs(ws)}
                onArchive={() => setArchiveWs(ws)}
              />
            ))}

            <DropdownMenu.Item
              onSelect={(e) => {
                e.preventDefault()
                setCreateWsOpen(true)
              }}
              className="mt-1 flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-fg-muted outline-none data-[highlighted]:bg-surface-muted"
            >
              <Plus className="h-3.5 w-3.5" strokeWidth={2} />
              <span>{t("workspaceCrud.workspace.createAction")}</span>
            </DropdownMenu.Item>

            {discoverable.length > 0 && (
              <>
                <DropdownMenu.Separator className="my-1 h-px bg-surface-muted" />
                <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-xs font-medium text-fg-subtle">
                  <Globe className="h-3 w-3" strokeWidth={1.75} />
                  {t("workspaceSwitcher.discoverLabel")}
                </DropdownMenu.Label>
                {discoverable.map((ws) => (
                  <div
                    key={ws.id}
                    className="group/row grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1 px-2 py-1.5"
                  >
                    <div className="col-span-2 flex min-w-0 flex-col">
                      <span className="break-words">{ws.name}</span>
                      <span className="truncate font-mono text-xs text-fg-faint">
                        {ws.slug}
                      </span>
                    </div>
                    <span className="text-xs text-fg-faint">
                      {t("workspaceSwitcher.memberCount", {
                        count: ws.member_count,
                      })}
                    </span>
                    {ws.has_pending_request ? (
                      <div className="flex items-center gap-1">
                        <span
                          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-warning"
                          title={t("workspaceSwitcher.pendingRequestTitle")}
                        >
                          <Clock className="h-3 w-3" strokeWidth={1.75} />
                          {t("workspaceSwitcher.pendingRequestBadge")}
                        </span>
                        <DropdownMenu.Item
                          onSelect={(e) => {
                            e.preventDefault()
                            withdrawJoinMut.mutate({ wsId: ws.id })
                          }}
                          disabled={withdrawJoinMut.isPending}
                          className="flex cursor-pointer items-center gap-1 rounded-sm px-2 py-1 text-xs text-fg-subtle outline-none data-[highlighted]:bg-surface-muted data-[disabled]:opacity-50"
                          title={t("workspaceSwitcher.withdrawRequestTitle")}
                        >
                          <X className="h-3 w-3" strokeWidth={1.75} />
                          <span>
                            {t("workspaceSwitcher.withdrawRequestAction")}
                          </span>
                        </DropdownMenu.Item>
                      </div>
                    ) : (
                      <DropdownMenu.Item
                        onSelect={(e) => {
                          e.preventDefault()
                          setJoinTarget(ws)
                        }}
                        className="flex cursor-pointer items-center gap-1 rounded-sm px-2 py-1 text-xs text-fg-muted outline-none data-[highlighted]:bg-surface-muted"
                        title={t("workspaceSwitcher.requestJoinTitle")}
                      >
                        <Send className="h-3 w-3" strokeWidth={1.75} />
                        <span>{t("workspaceSwitcher.requestJoinAction")}</span>
                      </DropdownMenu.Item>
                    )}
                  </div>
                ))}
                {discoverableTotal > discoverable.length && (
                  <DropdownMenu.Item
                    onSelect={(e) => {
                      e.preventDefault()
                      setDiscoverDialogOpen(true)
                    }}
                    className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-fg-muted outline-none data-[highlighted]:bg-surface-muted"
                  >
                    <Globe className="h-3.5 w-3.5" strokeWidth={1.75} />
                    <span>
                      {t("workspaceSwitcher.discoverViewAll", {
                        count: discoverableTotal,
                      })}
                    </span>
                  </DropdownMenu.Item>
                )}
              </>
            )}

            {joinToast && (
              <div className="mx-1 my-1 rounded-md border border-success-border bg-success-subtle px-2 py-1.5 text-xs text-success-emphasis">
                {joinToast}
              </div>
            )}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>

      <WorkspaceFormDialog
        open={createWsOpen}
        onCloseAutoFocus={dialogFocus.restoreFocus}
        onOpenChange={(open) => {
          if (!open) createWorkspaceMut.reset()
          setCreateWsOpen(open)
        }}
        mode="create"
        pending={createWorkspaceMut.isPending}
        error={createWorkspaceMut.error}
        onSubmit={({ name, visibility }) => {
          createWorkspaceMut.mutate(
            { name, visibility },
            {
              onSuccess: (data) => {
                setWorkspaceId(data.workspace.id)
                setCreateWsOpen(false)
              },
            }
          )
        }}
      />

      <WorkspaceFormDialog
        open={renameWs !== null}
        onCloseAutoFocus={dialogFocus.restoreFocus}
        onOpenChange={(open) => {
          if (!open) {
            updateWorkspaceMut.reset()
            setRenameWs(null)
          }
        }}
        mode="rename"
        initialName={renameWs?.name ?? ""}
        initialVisibility={renameWs?.visibility ?? "private"}
        pending={updateWorkspaceMut.isPending}
        error={updateWorkspaceMut.error}
        onSubmit={({ name, visibility }) => {
          if (!renameWs) return
          // PATCH only the changed fields.
          const body: { name?: string; visibility?: typeof visibility } = {}
          if (name !== renameWs.name) body.name = name
          if (visibility !== renameWs.visibility) body.visibility = visibility
          if (Object.keys(body).length === 0) {
            setRenameWs(null)
            return
          }
          updateWorkspaceMut.mutate(
            { wsId: renameWs.id, body },
            {
              onSuccess: () => {
                setRenameWs(null)
              },
            }
          )
        }}
      />

      <ConfirmArchiveDialog
        open={archiveWs !== null}
        onCloseAutoFocus={dialogFocus.restoreFocus}
        onOpenChange={(open) => {
          if (!open) {
            archiveWorkspaceMut.reset()
            setArchiveWs(null)
          }
        }}
        title={t("workspaceCrud.workspace.archiveTitle")}
        description={t("workspaceCrud.workspace.archiveDescription", {
          name: archiveWs?.name ?? "",
        }) + " " + t("workspaceCrud.workspace.archiveMarketplaceDependents")}
        pending={archiveWorkspaceMut.isPending}
        error={archiveWorkspaceMut.error}
        onConfirm={() => {
          if (!archiveWs) return
          const archivedId = archiveWs.id
          archiveWorkspaceMut.mutate(archivedId, {
            onSuccess: () => {
              if (wsId === archivedId) {
                setWorkspaceId(null)
              }
              setArchiveWs(null)
            },
          })
        }}
      />

      <JoinRequestDialog
        open={joinTarget !== null}
        onCloseAutoFocus={dialogFocus.restoreFocus}
        onOpenChange={(open) => {
          if (!open) {
            requestJoinMut.reset()
            setJoinTarget(null)
          }
        }}
        workspaceName={joinTarget?.name ?? ""}
        pending={requestJoinMut.isPending}
        error={requestJoinMut.error}
        onSubmit={({ reason }) => {
          if (!joinTarget) return
          const wsName = joinTarget.name
          requestJoinMut.mutate(
            { wsId: joinTarget.id, body: { reason } },
            {
              onSuccess: () => {
                setJoinToast(
                  t("workspaceSwitcher.joinSubmittedToast", { name: wsName })
                )
                setJoinTarget(null)
              },
            }
          )
        }}
      />

      <DiscoverWorkspacesDialog
        open={discoverDialogOpen}
        onCloseAutoFocus={dialogFocus.restoreFocus}
        onOpenChange={setDiscoverDialogOpen}
        onSelectToJoin={(ws) => {
          setDiscoverDialogOpen(false)
          setJoinTarget(ws)
        }}
      />
    </>
  )
}
