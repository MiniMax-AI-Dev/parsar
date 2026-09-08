import { type ReactNode, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Bot, Plus, Search, Wrench } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { DetailRail, RailLayout } from "../../components/ui/detail-rail"
import { FilterGroup, FilterMenu, FilterOption } from "../../components/ui/filter-menu"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ResourceAuditTimeline } from "../../components/admin/ResourceAuditTimeline"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Skeleton } from "../../components/ui/skeleton"
import { InitialTile } from "../../components/ui/ledger"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import { useAdminView, useAppRoute } from "../../lib/admin-router"
import { useAgentCreateDialog } from "../../lib/agent-create-return"
import { ApiError } from "../../lib/api-client"
import { createAgentConversation } from "../../lib/api-conversations"
import {
  useCreateAgent,
  useAgentDetail,
  useAgents,
  useDeleteAgent,
  useUpdateAgent,
  useUpdateAgentProfile,
} from "../../lib/api-agents"
import { useModels } from "../../lib/api-models"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useMarketplaceList } from "../../lib/api-marketplace"
import {
  AGENT_CONNECTOR_TYPES,
  searchAgents,
  agentConnectorKey,
  agentConnectorLabel,
  defaultModelOf,
} from "../../lib/agent-view-model"
import type { Agent } from "../../lib/api-types"
import { useToast } from "../../components/ui/toast"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { CreateAgentDialog } from "./CreateAgentDialog"
import { AgentConfigTab } from "./agents/AgentConfigTab"
import { AgentDetailActions } from "./agents/AgentDetailActions"
import { AgentExposureTab } from "./agents/AgentExposureTab"
import { AgentDynamicsTab } from "./agents/AgentDynamicsTab"
import { AgentsListTable } from "./agents/AgentsListTable"
import { AgentStatusBadge } from "./agents/AgentStatusBadge"
import { DeleteAgentDialog } from "./agents/DeleteAgentDialog"
import { DetailSection } from "./agents/DetailSection"

function usePendingCapability(workspaceID: string | null) {
  const id = new URLSearchParams(window.location.search).get("pendingCapability")
  const marketplaceQ = useMarketplaceList(workspaceID)
  const capability = (marketplaceQ.data ?? []).find((item) => item.id === id)
  return { id, capability }
}

/**
 * One-line notice under the topbar: a 14px icon (state lives there), ink
 * text, an optional control on the right. A hairline, never a tinted box.
 */
function InlineNotice({ icon, children, action }: { icon: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex min-h-9 shrink-0 items-center gap-2 border-b border-line px-4 py-1.5 text-sm text-fg" role="status">
      {icon}
      <span className="min-w-0 flex-1">{children}</span>
      {action}
    </div>
  )
}

function PendingCapabilityBanner({ children, onCancel, cancelLabel }: { children: ReactNode; onCancel: () => void; cancelLabel: string }) {
  return (
    <InlineNotice
      icon={<Wrench className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />}
      action={<Button variant="outline" size="sm" onClick={onCancel}>{cancelLabel}</Button>}
    >
      {children}
    </InlineNotice>
  )
}

export function AgentsPage() {
  const { t, i18n } = useTranslation("admin")
  const { navigate, entityId } = useAdminView()
  const wid = useWorkspaceId()
  const [keyword, setKeyword] = useState("")
  const [connectorFilter, setConnectorFilter] = useState("")
  const [createOpen, setCreateOpen] = useAgentCreateDialog()
  const [editAgent, setEditAgent] = useState<Agent | null>(null)
  const [cloneAgent, setCloneAgent] = useState<Agent | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null)
  const toast = useToast()
  const [chatPendingID, setChatPendingID] = useState<string | null>(null)
  const fmtAgo = useRelativeTime()

  const query = useAgents(wid)
  const createMut = useCreateAgent(wid)
  const cloneMut = useCreateAgent(wid)
  const modelsQ = useModels(wid)
  const updateMut = useUpdateAgent(wid)
  const updateProfileMut = useUpdateAgentProfile(wid)
  const deleteMut = useDeleteAgent(wid)
  const workspacesQ = useMyWorkspaces()
  const agents = useMemo(() => {
    const list = query.data?.agents ?? []
    // Sort: newest first (by enabled_at / created_at descending)
    return [...list].sort((a, b) => {
      const ta = a.enabled_at ?? ""
      const tb = b.enabled_at ?? ""
      return tb.localeCompare(ta)
    })
  }, [query.data])
  const models = useMemo(() => modelsQ.data?.models ?? [], [modelsQ.data])
  // Counted over the agents the search already narrowed to, so the menu
  // describes the list on screen rather than one the reader cannot see.
  // The known set is always listed — a connector with nothing on it is
  // usually what you opened the menu to check — plus any type actually in
  // use, because `connector_type` is unconstrained text on the server.
  const searched = useMemo(() => searchAgents(agents, keyword, models, t), [agents, keyword, models, t])
  const connectorCounts = useMemo(() => {
    const counts = new Map<string, number>(AGENT_CONNECTOR_TYPES.map((c) => [c, 0]))
    for (const agent of searched) {
      const key = agentConnectorKey(agent.connector_type)
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    return counts
  }, [searched])
  const connectorOptions = useMemo(() => [...connectorCounts.keys()], [connectorCounts])
  const currentWorkspace = workspacesQ.data?.workspaces.find((w) => w.id === wid)
  const workspaceRole = currentWorkspace?.role
  const workspaceName = currentWorkspace?.name
  const pendingCapability = usePendingCapability(wid)

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable
  const pageTitle = t("agents.page.title")

  // Hold the id through the rail's exit so closing animates instead of
  // vanishing — the same pattern every ledger uses.
  const [railID, setRailID] = useState<string | null>(entityId)
  if (entityId && entityId !== railID) setRailID(entityId)
  const agentRail = railID ? (
    <AgentDetailRail
      id={railID}
      open={!!entityId}
      onClose={() => navigate("agents")}
      onClosed={() => setRailID(null)}
    />
  ) : null

  async function startChatWith(a: Agent) {
    if (!wid || chatPendingID) return
    setChatPendingID(a.id)
    try {
      const conversation = await createAgentConversation(wid, a, i18n.language)
      navigate("conversations", { id: conversation.id, focus: "compose" })
    } catch {
      navigate("agents", { id: a.id })
    } finally {
      setChatPendingID(null)
    }
  }

  return (
    <AdminLayout activeMenu="agents" fullBleed>
      <RailLayout rail={agentRail}>
        <PageHeader
          className="static mx-0 mb-0 h-auto min-h-16 flex-wrap py-3"
          actionClassName="min-w-0 max-w-full flex-wrap [&>button]:h-auto [&>button]:min-h-7 [&>button]:max-w-full [&>button]:flex-wrap [&>button]:whitespace-normal [&>button]:py-1"
          title={pageTitle}
          subtitleFor="agents.page.title"
          action={
            <>
              <div className="relative w-72 min-w-0 max-w-full grow basis-40">
                <Search
                  className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                  strokeWidth={1.5}
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  placeholder={t("agents.search.placeholder")}
                  aria-label={t("agents.search.placeholder")}
                  className="pl-7"
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                />
              </div>
              <FilterMenu
                label={t("agents.filters.label")}
                summary={connectorFilter ? agentConnectorLabel(connectorFilter) : null}
              >
                <FilterGroup value={connectorFilter} onValueChange={setConnectorFilter}>
                  <FilterOption value="" label={t("agents.filters.allConnectors")} count={searched.length} />
                  {connectorOptions.map((connector) => (
                    <FilterOption
                      key={connector}
                      value={connector}
                      label={agentConnectorLabel(connector)}
                      count={connectorCounts.get(connector) ?? 0}
                    />
                  ))}
                </FilterGroup>
              </FilterMenu>
              <Button onClick={() => setCreateOpen(true)}>
                <Plus strokeWidth={1.5} aria-hidden="true" />
                {t("agents.actions.create")}
              </Button>
            </>
          }
        />
        {pendingCapability.id && (
          <PendingCapabilityBanner
            cancelLabel={t("agents.pendingCapability.cancel")}
            onCancel={() => navigate("agents", { pendingCapability: null })}
          >
            {pendingCapability.capability
              ? t("agents.pendingCapability.banner", { name: pendingCapability.capability.name, source: pendingCapability.capability.source_workspace_name ?? "—" })
              : t("agents.pendingCapability.loading")}
          </PendingCapabilityBanner>
        )}
        {!wid ? (
          <div className="px-6"><ScopeRequiredState scope="workspace" resourceName={pageTitle} /></div>
        ) : query.isLoading ? (
          <AgentsLoadingSkeleton />
        ) : err ? (
          <div className="px-6 pt-6">
            <ErrorState
              title={
                isUnreachable
                  ? t("agents.loadError.unreachable.title")
                  : t("agents.loadError.title")
              }
              description={isUnreachable ? t("agents.loadError.unreachable.description") : t("agents.loadError.description")}
              detail={!isUnreachable && err instanceof Error ? err.message : undefined}
              hint={
                isUnreachable
                  ? t("agents.loadError.unreachable.hint")
                  : t("agents.loadError.hint")
              }
              onRetry={() => void query.refetch()}
            />
          </div>
        ) : agents.length === 0 ? (
          <EmptyState
            icon={Bot}
            title={t("agents.empty.title")}
            description={t("agents.empty.description")}
          />
        ) : (
          <AgentsListTable
            agents={agents}
            models={models}
            keyword={keyword}
            connectorFilter={connectorFilter}
            onClearFilters={() => {
              setKeyword("")
              setConnectorFilter("")
            }}
            selectedID={entityId}
            chatPendingID={chatPendingID}
            deletePending={deleteMut.isPending}
            formatRelativeTime={fmtAgo}
            onOpenAgent={(agent) => navigate("agents", { id: agent.id === entityId ? null : agent.id })}
            onChat={(agent) => void startChatWith(agent)}
            onEdit={setEditAgent}
            onClone={setCloneAgent}
            onDelete={setDeleteTarget}
          />
        )}
      </RailLayout>

      <CreateAgentDialog
        open={createOpen}
        mode="create"
        workspaceID={wid}
        workspaceName={workspaceName}
        workspaceRole={workspaceRole}
        models={models}
        pending={createMut.isPending}
        error={createMut.error}
        onOpenChange={(v) => {
          setCreateOpen(v)
          if (!v) createMut.reset()
        }}
        onSubmit={({ body }) => {
          createMut.mutate(body as Parameters<typeof createMut.mutate>[0], {
            onSuccess: (created) => {
              setCreateOpen(false)
              void (async () => {
                if (!wid) {
                  navigate("agents", { id: created.id })
                  return
                }
                try {
                  const conversation = await createAgentConversation(wid, created, i18n.language)
                  navigate("conversations", { id: conversation.id, focus: "compose" })
                } catch {
                  navigate("agents", { id: created.id })
                }
              })()
            },
          })
        }}
      />

      <CreateAgentDialog
        open={editAgent !== null}
        mode="edit"
        workspaceID={wid}
        workspaceName={workspaceName}
        workspaceRole={workspaceRole}
        models={models}
        agent={editAgent ?? undefined}
        pending={updateMut.isPending || updateProfileMut.isPending}
        error={updateMut.error ?? updateProfileMut.error}
        onOpenChange={(v) => {
          if (!v) {
            setEditAgent(null)
            updateMut.reset()
            updateProfileMut.reset()
          }
        }}
        onSubmit={({ agentID, body, agentProfile }) => {
          if (!agentID) return
          void (async () => {
            try {
              await updateMut.mutateAsync({ agentID, body })
              if (agentProfile) {
                await updateProfileMut.mutateAsync({ agentID, body: agentProfile })
              }
              setEditAgent(null)
            } catch {
              // React Query owns the surfaced error; keep the dialog open.
            }
          })()
        }}
      />

      <CreateAgentDialog
        open={cloneAgent !== null}
        mode="create"
        workspaceID={wid}
        workspaceName={workspaceName}
        workspaceRole={workspaceRole}
        models={models}
        agent={cloneAgent ?? undefined}
        pending={cloneMut.isPending}
        error={cloneMut.error}
        onOpenChange={(v) => {
          if (!v) {
            setCloneAgent(null)
            cloneMut.reset()
          }
        }}
        onSubmit={({ body }) => {
          cloneMut.mutate(body as Parameters<typeof cloneMut.mutate>[0], {
            onSuccess: (created) => {
              setCloneAgent(null)
              toast.show(t("agents.listActions.clonedToast", { name: created.name }))
            },
          })
        }}
      />

      <DeleteAgentDialog
        agent={deleteTarget}
        pending={deleteMut.isPending}
        error={deleteMut.error}
        onCancel={() => {
          setDeleteTarget(null)
          deleteMut.reset()
        }}
        onConfirm={() => {
          if (!deleteTarget) return
          const name = deleteTarget.name
          deleteMut.mutate(deleteTarget.id, {
            onSuccess: () => {
              setDeleteTarget(null)
              toast.show(t("agents.delete.deletedToast", { name }))
            },
          })
        }}
      />
    </AdminLayout>
  )
}

function AgentsLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-[18px] w-[18px] rounded" />
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
  )
}

/**
 * An Agent read in the rail beside the list. It is the console's one true
 * workplace — three tabs, an editable config, an audit trail — so it is the
 * rail's widest brief: identity in the header, its verbs in the footer, and
 * the expand button for the times the config form wants room.
 */
export function AgentDetailRail({ id, open, onClose, onClosed }: {
  id: string
  open: boolean
  onClose: () => void
  onClosed: () => void
}) {
  const { t } = useTranslation("admin")
  const { navigate, tab: requestedTab } = useAdminView()
  const { railView } = useAppRoute()
  const wid = useWorkspaceId()
  const toast = useToast()

  const query = useAgentDetail(wid, id)
  const modelsQ = useModels(wid)
  const workspacesQ = useMyWorkspaces()
  const agent = query.data
  const models = modelsQ.data?.models ?? []
  const currentWorkspace = workspacesQ.data?.workspaces.find((w) => w.id === wid)
  const workspaceRole = currentWorkspace?.role
  const pendingCapability = usePendingCapability(wid)

  // Switching to another agent swaps the rail's content rather than replaying
  // its entrance, so the per-agent state is reset here instead of by a remount.
  const [shownID, setShownID] = useState(id)
  if (id !== shownID) setShownID(id)

  if (query.isLoading || query.error || !agent) {
    return (
      <DetailRail
        open={open}
        onClose={onClose}
        onClosed={onClosed}
        aria-label={t("agents.page.title")}
        header={<Skeleton className="h-3 w-40" />}
      >
        {query.isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-3 w-full" />)}
          </div>
        ) : query.error ? (
          <ErrorState
            title={t("agents.detail.loadError.title")}
            description={t("agents.detail.loadError.description")}
            detail={query.error instanceof Error ? query.error.message : undefined}
            onRetry={() => void query.refetch()}
          />
        ) : (
          <EmptyState icon={Bot} title={t("agents.empty.title")} description={t("agents.empty.description")} />
        )}
      </DetailRail>
    )
  }

  const model = defaultModelOf(agent, models, t("agents.modelUnavailable"))
  return (
    <DetailRail
      open={open}
      onClose={onClose}
      onClosed={onClosed}
      aria-label={agent.name}
      header={
        <>
          <InitialTile name={agent.name} />
          <span className="min-w-0 truncate text-base font-medium text-fg">{agent.name}</span>
          <AgentStatusBadge status={agent.status} />
        </>
      }
      footer={
        <AgentDetailActions
          agent={agent}
          workspaceID={wid}
          workspaceName={currentWorkspace?.name}
          workspaceRole={workspaceRole}
          models={models}
          onToast={toast.show}
        />
      }
    >
      {pendingCapability.id && (
        <PendingCapabilityBanner
          cancelLabel={t("agents.pendingCapability.cancel")}
          onCancel={() => navigate("agents", { id: agent.id, tab: "config", pendingCapability: null, view: railView })}
        >
          {t("agents.pendingCapability.detailBanner", {
            name: pendingCapability.capability?.name ?? pendingCapability.id,
            source: pendingCapability.capability?.source_workspace_name ?? "—",
          })}
        </PendingCapabilityBanner>
      )}

      <Tabs
        value={requestedTab ?? "dynamics"}
        onValueChange={(tab) => navigate("agents", { id: agent.id, tab, view: railView })}
      >
        <TabsList className="flex w-full">
          <TabsTrigger value="dynamics" className="flex-1">{t("agents.detail.tabs.dynamics")}</TabsTrigger>
          <TabsTrigger value="config" className="flex-1">{t("agents.detail.tabs.config")}</TabsTrigger>
          <TabsTrigger value="exposure" className="flex-1">{t("agents.detail.tabs.exposure")}</TabsTrigger>
          <TabsTrigger value="audit" className="flex-1">{t("agents.detail.tabs.audit")}</TabsTrigger>
        </TabsList>

        <TabsContent value="dynamics">
          <AgentDynamicsTab workspaceID={wid} agent={agent} />
        </TabsContent>

        <TabsContent value="config">
          <AgentConfigTab
            agent={agent}
            workspaceID={wid}
            workspaceRole={workspaceRole}
            modelLabel={model}
            onToast={toast.show}
          />
        </TabsContent>

        <TabsContent value="exposure">
          <AgentExposureTab
            agent={agent}
            workspaceID={wid}
            canEdit={workspaceRole === "owner" || workspaceRole === "admin"}
            onToast={toast.show}
          />
        </TabsContent>

        <TabsContent value="audit">
          <DetailSection title={t("agents.detail.audit.title")}>
            <ResourceAuditTimeline wsId={wid} targetType="agent" targetID={agent.id} />
          </DetailSection>
        </TabsContent>
      </Tabs>
    </DetailRail>
  )
}
