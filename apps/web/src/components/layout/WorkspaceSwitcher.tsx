import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import {
  Archive,
  Check,
  ChevronsUpDown,
  Clock,
  FolderKanban,
  Globe,
  Layers,
  Pencil,
  Plus,
  Send,
  X,
} from "lucide-react"
import { useEffect, useState } from "react"
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import {
  setProjectId,
  setWorkspaceId,
  useProjectId,
  useWorkspaceId,
} from "../../lib/workspace"
import {
  useArchiveProject,
  useArchiveWorkspace,
  useCreateProject,
  useCreateWorkspace,
  useDiscoverableWorkspaces,
  useMyWorkspaces,
  useRequestJoinWorkspace,
  useUpdateProject,
  useUpdateWorkspace,
  useWithdrawJoinRequest,
  useWorkspaceProjects,
} from "../../lib/api-workspaces"
import type {
  DiscoverableWorkspace,
  UserWorkspace,
  WorkspaceProject,
} from "../../lib/api-types"
import {
  ConfirmArchiveDialog,
  JoinRequestDialog,
  ProjectFormDialog,
  WorkspaceFormDialog,
} from "./WorkspaceCrudDialogs"
import { DiscoverWorkspacesDialog } from "./DiscoverWorkspacesDialog"

function shortId(id: string | null | undefined): string {
  if (!id) return ""
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

export function WorkspaceSwitcher() {
  const { t } = useTranslation("common")
  const wsId = useWorkspaceId()
  const projectId = useProjectId()
  const workspacesQuery = useMyWorkspaces()
  const projectsQuery = useWorkspaceProjects(wsId)
  // Cap at 5; overflow opens the full DiscoverWorkspacesDialog so the
  // dropdown can't be blown up by hundreds of workspaces.
  const discoverableQuery = useDiscoverableWorkspaces({ limit: 5 })
  const [discoverDialogOpen, setDiscoverDialogOpen] = useState(false)

  const workspaces = workspacesQuery.data?.workspaces ?? []
  const projects = projectsQuery.data?.projects ?? []
  const discoverable = discoverableQuery.data?.workspaces ?? []
  const discoverableTotal = discoverableQuery.data?.total ?? discoverable.length
  const currentWorkspace = workspaces.find((w) => w.id === wsId)
  const currentProject = projects.find((p) => p.id === projectId)

  // Self-heal: stale wsId from localStorage (archived ws or old dev seed)
  // would leave currentWorkspace undefined; auto-pick the first one.
  useEffect(() => {
    if (workspacesQuery.isLoading || workspaces.length === 0) return
    if (wsId && workspaces.some((w) => w.id === wsId)) return
    setWorkspaceId(workspaces[0].id)
  }, [wsId, workspaces, workspacesQuery.isLoading])

  // Dialog state lives here so Radix Dropdown's focus trap doesn't fight
  // the dialog's focus trap.
  const [createWsOpen, setCreateWsOpen] = useState(false)
  const [renameWs, setRenameWs] = useState<UserWorkspace | null>(null)
  const [archiveWs, setArchiveWs] = useState<UserWorkspace | null>(null)
  const [createProjOpen, setCreateProjOpen] = useState(false)
  const [renameProj, setRenameProj] = useState<WorkspaceProject | null>(null)
  const [archiveProj, setArchiveProj] = useState<WorkspaceProject | null>(null)
  const [joinTarget, setJoinTarget] = useState<DiscoverableWorkspace | null>(
    null
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
  const createProjectMut = useCreateProject()
  const updateProjectMut = useUpdateProject()
  const archiveProjectMut = useArchiveProject()
  const requestJoinMut = useRequestJoinWorkspace()
  const withdrawJoinMut = useWithdrawJoinRequest()

  const triggerLabel = currentWorkspace?.name
    ? currentWorkspace.name
    : wsId
      ? `WS · ${shortId(wsId)}`
      : t("workspaceSwitcher.demoWorkspace")

  const triggerProjectLabel = currentProject?.name
    ? currentProject.name
    : projectId
      ? `P · ${shortId(projectId)}`
      : null

  return (
    <>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button
            type="button"
            aria-label={t("workspaceSwitcher.triggerAriaLabel")}
            className="ml-2 inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-fg-muted hover:bg-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-200 data-[state=open]:bg-surface-muted"
          >
            <span className="font-medium">{triggerLabel}</span>
            {triggerProjectLabel && (
              <span className="rounded bg-surface-muted px-1 py-0 text-xs font-mono text-fg-muted">
                {triggerProjectLabel}
              </span>
            )}
            {!wsId && (
              <span
                title={t("workspace.mockTooltip")}
                className="rounded bg-warning-subtle px-1 py-0 text-xs font-medium text-warning"
              >
                {t("workspace.mockBadge")}
              </span>
            )}
            <ChevronsUpDown className="h-3 w-3 text-fg-faint" strokeWidth={1.75} />
          </button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={6}
            className="z-50 min-w-[300px] overflow-hidden rounded-md border border-line bg-surface p-1 text-sm text-fg-muted shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
          >
            <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-xs font-medium uppercase tracking-wider text-fg-subtle">
              <Layers className="h-3 w-3" strokeWidth={1.75} />
              {t("workspaceSwitcher.workspaceLabel")}
            </DropdownMenu.Label>

            {workspaces.length === 0 && (
              <div className="px-2 py-2 text-sm text-fg-faint">
                {workspacesQuery.isLoading
                  ? t("states.loading")
                  : t("workspaceSwitcher.noWorkspaces")}
              </div>
            )}

            {workspaces.map((ws) => {
              const isActive = ws.id === wsId
              return (
                <div
                  key={ws.id}
                  className="group/row flex items-center gap-1"
                >
                  <DropdownMenu.Item
                    onSelect={() => {
                      if (ws.id === wsId) return
                      setWorkspaceId(ws.id)
                      // Switching workspace invalidates the picked project.
                      setProjectId(null)
                    }}
                    className={cn(
                      "flex flex-1 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none data-[highlighted]:bg-surface-muted",
                      isActive && "font-medium text-fg"
                    )}
                  >
                    <div className="flex flex-1 flex-col min-w-0">
                      <span className="truncate">{ws.name}</span>
                      <span className="truncate font-mono text-xs text-fg-faint">
                        {ws.slug}
                      </span>
                    </div>
                    <span className="text-xs uppercase tracking-wider text-fg-faint">
                      {ws.role}
                    </span>
                    {isActive && (
                      <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={2} />
                    )}
                  </DropdownMenu.Item>
                  <RowAction
                    title={t("workspaceCrud.workspace.renameTitle")}
                    onSelect={() => setRenameWs(ws)}
                    icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />}
                  />
                  <RowAction
                    title={t("workspaceCrud.workspace.archiveTitle")}
                    onSelect={() => setArchiveWs(ws)}
                    icon={<Archive className="h-3.5 w-3.5" strokeWidth={1.75} />}
                    danger
                  />
                </div>
              )
            })}

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
                <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-xs font-medium uppercase tracking-wider text-fg-subtle">
                  <Globe className="h-3 w-3" strokeWidth={1.75} />
                  {t("workspaceSwitcher.discoverLabel")}
                </DropdownMenu.Label>
                {discoverable.map((ws) => (
                  <div
                    key={ws.id}
                    className="group/row flex items-center gap-1"
                  >
                    <div className="flex flex-1 items-center gap-2 rounded-sm px-2 py-1.5">
                      <div className="flex flex-1 flex-col min-w-0">
                        <span className="truncate">{ws.name}</span>
                        <span className="truncate font-mono text-xs text-fg-faint">
                          {ws.slug}
                        </span>
                      </div>
                      <span className="text-xs text-fg-faint">
                        {t("workspaceSwitcher.memberCount", {
                          count: ws.member_count,
                        })}
                      </span>
                    </div>
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

            <DropdownMenu.Separator className="my-1 h-px bg-surface-muted" />

            <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-xs font-medium uppercase tracking-wider text-fg-subtle">
              <FolderKanban className="h-3 w-3" strokeWidth={1.75} />
              {currentWorkspace?.name
                ? t("workspaceSwitcher.projectsInLabel", {
                    workspace: currentWorkspace.name,
                  })
                : t("workspaceSwitcher.projectsLabel")}
            </DropdownMenu.Label>

            {!wsId && (
              <div className="px-2 py-2 text-sm text-fg-faint">
                {t("workspaceSwitcher.pickWorkspaceFirst")}
              </div>
            )}

            {wsId && projects.length === 0 && (
              <div className="px-2 py-2 text-sm text-fg-faint">
                {projectsQuery.isLoading
                  ? t("states.loading")
                  : t("workspaceSwitcher.noProjects")}
              </div>
            )}

            {wsId &&
              projects.map((p) => {
                const isActive = p.id === projectId
                return (
                  <div key={p.id} className="group/row flex items-center gap-1">
                    <DropdownMenu.Item
                      onSelect={() => {
                        if (p.id === projectId) return
                        setProjectId(p.id)
                      }}
                      className={cn(
                        "flex flex-1 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none data-[highlighted]:bg-surface-muted",
                        isActive && "font-medium text-fg"
                      )}
                    >
                      <div className="flex flex-1 flex-col min-w-0">
                        <span className="truncate">{p.name}</span>
                        <span className="truncate font-mono text-xs text-fg-faint">
                          {p.slug}
                        </span>
                      </div>
                      {isActive && (
                        <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={2} />
                      )}
                    </DropdownMenu.Item>
                    <RowAction
                      title={t("workspaceCrud.project.renameTitle")}
                      onSelect={() => setRenameProj(p)}
                      icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />}
                    />
                    <RowAction
                      title={t("workspaceCrud.project.archiveTitle")}
                      onSelect={() => setArchiveProj(p)}
                      icon={<Archive className="h-3.5 w-3.5" strokeWidth={1.75} />}
                      danger
                    />
                  </div>
                )
              })}

            {wsId && (
              <DropdownMenu.Item
                onSelect={(e) => {
                  e.preventDefault()
                  setCreateProjOpen(true)
                }}
                className="mt-1 flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-fg-muted outline-none data-[highlighted]:bg-surface-muted"
              >
                <Plus className="h-3.5 w-3.5" strokeWidth={2} />
                <span>{t("workspaceCrud.project.createAction")}</span>
              </DropdownMenu.Item>
            )}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>

      <WorkspaceFormDialog
        open={createWsOpen}
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
                setProjectId(null)
                setCreateWsOpen(false)
              },
            }
          )
        }}
      />

      <WorkspaceFormDialog
        open={renameWs !== null}
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
                setProjectId(null)
              }
              setArchiveWs(null)
            },
          })
        }}
      />

      <ProjectFormDialog
        open={createProjOpen}
        onOpenChange={(open) => {
          if (!open) createProjectMut.reset()
          setCreateProjOpen(open)
        }}
        mode="create"
        pending={createProjectMut.isPending}
        error={createProjectMut.error}
        onSubmit={({ name, description }) => {
          if (!wsId) return
          createProjectMut.mutate(
            {
              wsId,
              body: {
                name,
                description: description || undefined,
              },
            },
            {
              onSuccess: (data) => {
                setProjectId(data.project.id)
                setCreateProjOpen(false)
              },
            }
          )
        }}
      />

      <ProjectFormDialog
        open={renameProj !== null}
        onOpenChange={(open) => {
          if (!open) {
            updateProjectMut.reset()
            setRenameProj(null)
          }
        }}
        mode="rename"
        initialName={renameProj?.name ?? ""}
        initialDescription={renameProj?.description ?? ""}
        pending={updateProjectMut.isPending}
        error={updateProjectMut.error}
        onSubmit={({ name, description }) => {
          if (!renameProj) return
          const body: {
            name?: string
            description?: string
          } = {}
          if (name !== renameProj.name) body.name = name
          if (description !== renameProj.description) body.description = description
          if (Object.keys(body).length === 0) {
            setRenameProj(null)
            return
          }
          updateProjectMut.mutate(
            { pid: renameProj.id, wsId, body },
            {
              onSuccess: () => {
                setRenameProj(null)
              },
            }
          )
        }}
      />

      <ConfirmArchiveDialog
        open={archiveProj !== null}
        onOpenChange={(open) => {
          if (!open) {
            archiveProjectMut.reset()
            setArchiveProj(null)
          }
        }}
        title={t("workspaceCrud.project.archiveTitle")}
        description={t("workspaceCrud.project.archiveDescription", {
          name: archiveProj?.name ?? "",
        })}
        pending={archiveProjectMut.isPending}
        error={archiveProjectMut.error}
        onConfirm={() => {
          if (!archiveProj) return
          const archivedId = archiveProj.id
          archiveProjectMut.mutate(
            { pid: archivedId, wsId },
            {
              onSuccess: () => {
                if (projectId === archivedId) {
                  setProjectId(null)
                }
                setArchiveProj(null)
              },
            }
          )
        }}
      />

      <JoinRequestDialog
        open={joinTarget !== null}
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
        onOpenChange={setDiscoverDialogOpen}
        onSelectToJoin={(ws) => {
          setDiscoverDialogOpen(false)
          setJoinTarget(ws)
        }}
      />
    </>
  )
}

interface RowActionProps {
  title: string
  icon: ReactNode
  onSelect: () => void
  danger?: boolean
}

function RowAction({ title, icon, onSelect, danger }: RowActionProps) {
  return (
    <DropdownMenu.Item
      onSelect={(e) => {
        e.preventDefault()
        onSelect()
      }}
      title={title}
      aria-label={title}
      className={cn(
        "invisible flex h-7 w-7 cursor-pointer items-center justify-center rounded outline-none text-fg-faint hover:text-fg-muted data-[highlighted]:text-fg-muted group-hover/row:visible",
        danger && "hover:text-danger data-[highlighted]:text-danger"
      )}
    >
      {icon}
    </DropdownMenu.Item>
  )
}
