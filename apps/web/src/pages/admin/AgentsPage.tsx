import { type ReactNode, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Bot,
  Plus,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ResourceAuditTimeline } from "../../components/admin/ResourceAuditTimeline"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Skeleton } from "../../components/ui/skeleton"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import { useAdminView } from "../../lib/admin-router"
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
import { defaultModelOf } from "../../lib/agent-view-model"
import type { Agent } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { CreateAgentDialog } from "./CreateAgentDialog"
import { AgentConfigTab } from "./agents/AgentConfigTab"
import { AgentDetailActions } from "./agents/AgentDetailActions"
import { AgentDynamicsTab } from "./agents/AgentDynamicsTab"
import { AgentsListTable } from "./agents/AgentsListTable"
import { AgentStatusBadge } from "./agents/AgentStatusBadge"
import { DeleteAgentDialog } from "./agents/DeleteAgentDialog"

function usePendingCapability(workspaceID: string | null) {
  const id = new URLSearchParams(window.location.search).get("pendingCapability")
  const marketplaceQ = useMarketplaceList(workspaceID)
  const capability = (marketplaceQ.data ?? []).find((item) => item.id === id)
  return { id, capability }
}

function PendingCapabilityBanner({ children, onCancel, cancelLabel }: { children: ReactNode; onCancel: () => void; cancelLabel: string }) {
  return (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-info-border bg-info-subtle px-3 py-2 text-sm text-info-emphasis">
      <span>{children}</span>
      <Button variant="outline" size="sm" onClick={onCancel}>{cancelLabel}</Button>
    </div>
  )
}

export function AgentsPage() {
  const { t, i18n } = useTranslation("admin")
  const { navigate } = useAdminView()
  const wid = useWorkspaceId()
  const [keyword, setKeyword] = useState("")
  const [createOpen, setCreateOpen] = useState(false)
  const [editAgent, setEditAgent] = useState<Agent | null>(null)
  const [cloneAgent, setCloneAgent] = useState<Agent | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null)
  const [toast, setToast] = useState<string | null>(null)
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
  const agents = useMemo(() => query.data?.agents ?? [], [query.data])
  const models = modelsQ.data?.models ?? []
  const currentWorkspace = workspacesQ.data?.workspaces.find((w) => w.id === wid)
  const workspaceRole = currentWorkspace?.role
  const workspaceName = currentWorkspace?.name
  const pendingCapability = usePendingCapability(wid)

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

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
    <AdminLayout activeMenu="agents">
      <PageHeader
        title={t("agents.page.title")}
        description={t("agents.page.description")}
        action={
          <Button size="sm" shape="pill" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5" strokeWidth={2} /> {t("agents.actions.create")}
          </Button>
        }
      />
      {toast && (
        <div className="mb-4 rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
          {toast}
        </div>
      )}
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
        <ScopeRequiredState scope="workspace" resourceName={t("agents.page.title")} />
      ) : query.isLoading ? (
        <AgentsLoadingSkeleton />
      ) : err ? (
        <ErrorState
          title={
            isUnreachable
              ? t("agents.loadError.unreachable.title")
              : t("agents.loadError.title")
          }
          description={
            isUnreachable
              ? t("agents.loadError.unreachable.description")
              : err instanceof Error
                ? err.message
                : t("agents.loadError.description")
          }
          hint={
            isUnreachable
              ? t("agents.loadError.unreachable.hint")
              : t("agents.loadError.hint")
          }
          onRetry={() => void query.refetch()}
        />
      ) : agents.length === 0 ? (
        <EmptyState
          icon={Bot}
          title={t("agents.empty.title")}
          description={t("agents.empty.description")}
          action={
            <Button size="sm" shape="pill" onClick={() => setCreateOpen(true)}>
              <Plus className="h-3.5 w-3.5" /> {t("agents.actions.create")}
            </Button>
          }
        />
      ) : (
        <AgentsListTable
          agents={agents}
          models={models}
          keyword={keyword}
          chatPendingID={chatPendingID}
          deletePending={deleteMut.isPending}
          formatRelativeTime={fmtAgo}
          onKeywordChange={setKeyword}
          onOpenAgent={(agent) => navigate("agents", { id: agent.id })}
          onChat={(agent) => void startChatWith(agent)}
          onEdit={setEditAgent}
          onClone={setCloneAgent}
          onDelete={setDeleteTarget}
        />
      )}

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
              setToast(t("agents.listActions.clonedToast", { name: created.name }))
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
              setToast(t("agents.delete.deletedToast", { name }))
            },
          })
        }}
      />
    </AdminLayout>
  )
}

function AgentsLoadingSkeleton() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-8 w-72" />
      </div>
      <div className="space-y-2 rounded-lg border border-line bg-surface p-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="h-8 w-8 rounded" />
            <div className="flex-1 space-y-1.5">
              <Skeleton className="h-3 w-1/3" />
              <Skeleton className="h-2.5 w-1/2" />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function AgentDetailPage({ id }: { id: string }) {
  const { t } = useTranslation("admin")
  const { navigate, tab: requestedTab } = useAdminView()
  const wid = useWorkspaceId()
  const [toast, setToast] = useState<string | null>(null)

  const query = useAgentDetail(wid, id)
  const modelsQ = useModels(wid)
  const workspacesQ = useMyWorkspaces()
  const agent = query.data
  const models = modelsQ.data?.models ?? []
  const currentWorkspace = workspacesQ.data?.workspaces.find((w) => w.id === wid)
  const workspaceRole = currentWorkspace?.role
  const pendingCapability = usePendingCapability(wid)

  if (query.isLoading) {
    return (
      <AdminLayout activeMenu="agents">
        <AgentsLoadingSkeleton />
      </AdminLayout>
    )
  }

  if (query.error) {
    return (
      <AdminLayout activeMenu="agents">
        <ErrorState
          title={t("agents.detail.loadError.title")}
          description={query.error instanceof Error ? query.error.message : t("agents.detail.loadError.description")}
          onRetry={() => void query.refetch()}
        />
      </AdminLayout>
    )
  }

  if (!agent) {
    return (
      <AdminLayout activeMenu="agents">
        <EmptyState
          icon={Bot}
          title={t("agents.empty.title")}
          description={t("agents.empty.description")}
        />
      </AdminLayout>
    )
  }

  const model = defaultModelOf(agent, models, t("agents.modelUnavailable"))
  return (
    <AdminLayout activeMenu="agents">
      <PageHeader
        backLink={
          <button
            onClick={() => navigate("agents")}
            className="hover:text-fg hover:underline"
          >
            ← {t("agents.page.title")}
          </button>
        }
        title={agent.name}
        description={agent.description}
        action={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <AgentStatusBadge status={agent.status} />
            <AgentDetailActions
              agent={agent}
              workspaceID={wid}
              workspaceName={currentWorkspace?.name}
              workspaceRole={workspaceRole}
              models={models}
              onToast={setToast}
            />
          </div>
        }
      />

      {toast && (
        <div className="mb-4 rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
          {toast}
        </div>
      )}
      {pendingCapability.id && (
        <PendingCapabilityBanner
          cancelLabel={t("agents.pendingCapability.cancel")}
          onCancel={() => navigate("agents", { id: agent.id, tab: "config", pendingCapability: null })}
        >
          {t("agents.pendingCapability.detailBanner", {
            name: pendingCapability.capability?.name ?? pendingCapability.id,
            source: pendingCapability.capability?.source_workspace_name ?? "—",
          })}
        </PendingCapabilityBanner>
      )}

      <Tabs
        value={requestedTab ?? "dynamics"}
        onValueChange={(tab) => navigate("agents", { id: agent.id, tab })}
      >
        <TabsList>
          <TabsTrigger value="dynamics">{t("agents.detail.tabs.dynamics")}</TabsTrigger>
          <TabsTrigger value="config">{t("agents.detail.tabs.config")}</TabsTrigger>
          <TabsTrigger value="audit">{t("agents.detail.tabs.audit")}</TabsTrigger>
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
            onToast={setToast}
          />
        </TabsContent>

        <TabsContent value="audit">
          <Card title={t("agents.detail.audit.title")}>
            <ResourceAuditTimeline
              wsId={wid}
              targetType="agent"
              targetID={agent.id}
            />
          </Card>
        </TabsContent>
      </Tabs>
    </AdminLayout>
  )
}

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <h3 className="mb-3 text-base font-semibold text-fg">{title}</h3>
      {children}
    </section>
  )
}
