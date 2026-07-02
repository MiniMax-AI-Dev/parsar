import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import * as Tooltip from "@radix-ui/react-tooltip"
import {
  ArrowUpRight,
  Bot,
  Cable,
  Copy,
  Database,
  Loader2,
  MessageSquare,
  Pencil,
  Plus,
  Trash2,
  Search,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ResourceAuditTimeline } from "../../components/admin/ResourceAuditTimeline"
import { SandboxPanel } from "../../components/admin/SandboxPanel"
import {
  FeishuConnectorPanel,
  readFeishuConfigFromAgent,
} from "../../components/admin/FeishuConnectorPanel"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Skeleton } from "../../components/ui/skeleton"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../components/ui/alert-dialog"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/ui/table"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { createConversation } from "../../lib/api-conversations"
import {
  useCreateAgent,
  useAgents,
  useSetAgentStatus,
  useUpdateAgent,
  useUpdateAgentVisibility,
  useUpdateAgentProfile,
  useAgentMetrics,
  useAgentRuns,
  type AgentMetrics,
  type AgentVisibility,
} from "../../lib/api-agents"
import { useModels } from "../../lib/api-models"
import { useSandboxBinding } from "../../lib/api-sandbox"
import {
  useCapabilitiesQuery,
  useCapabilityVersionsQuery,
  useDeleteAgentCapabilityMutation,
  useEnableAgentCapabilityMutation,
  useAgentCapabilitiesQuery,
  useToggleBuiltinCapabilityMutation,
} from "../../lib/api-capabilities"
import { useMyCredentials } from "../../lib/api-credentials"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useMarketplaceList } from "../../lib/api-marketplace"
import type { Agent, AgentCapability, AgentRunStatus, AgentRunSummary, Capability, CapabilityVersion, Model, UserCredential } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"
import { CreateAgentDialog } from "./CreateAgentDialog"
import { CapabilityTypeBadge } from "./CapabilitiesPage"
import { UpgradeCapabilityDialog } from "./capabilities/UpgradeCapabilityDialog"
import { credentialKindLabel } from "./capability-ui"

/* ------------------------------------------------------------------ */
/*  View-model derived from Agent                              */
/* ------------------------------------------------------------------ */

/**
 * Resolve the default-model display label. The agent stores only
 * `default_model_id` (a UUID); falls back through legacy
 * `model_id` / `profile.model_id` shapes for older rows.
 */
function defaultModelOf(a: Agent, models: Model[]): string {
  const cfg = (a.config as Record<string, unknown> | undefined) ?? {}
  const profile = (cfg.profile ?? {}) as Record<string, unknown>
  const id = String(cfg.default_model_id ?? cfg.model_id ?? profile.model_id ?? "")
  if (!id) return "—"
  const found = models.find((m) => m.id === id)
  if (!found) return id // model deleted or workspace mismatch — show id over silent "—"
  return found.name || found.model_key || id
}

function connectorLabel(t: string): string {
  if (t === "agent_daemon") return "Agent Daemon"
  if (t === "http-agent" || t === "http") return "HTTP Agent"
  return t
}

function runtimeOf(a: Agent): "local" | "sandbox" {
  // Backend zeroes out `a.runtime` for agent_daemon rows, so we can't
  // trust it alone — for agent_daemon the placement lives in
  // pa.config.daemon_mode. "unknown" fallback maps to "sandbox" so the
  // detail label still renders for very old rows.
  const placement = executionPlacement(a)
  return placement === "local" ? "local" : "sandbox"
}

function tagsOf(a: Agent): string[] {
  const profile = ((a.config as Record<string, unknown> | undefined)?.profile
    ?? {}) as Record<string, unknown>
  return Array.isArray(profile.skills) ? profile.skills.filter((v): v is string => typeof v === "string") : []
}

function starterConversationTitle(agentName: string, language: string): string {
  const name = agentName.trim()
  if (!name) return ""
  return language.startsWith("zh") ? `和 ${name} 对话` : `Chat with ${name}`
}

/* ------------------------------------------------------------------ */
/*  Status badge                                                       */
/* ------------------------------------------------------------------ */

function AgentStatusBadge({ status }: { status: Agent["status"] }) {
  const { t } = useTranslation("admin")
  if (status === "active") return <Badge variant="success" dot>{t("agents.status.active")}</Badge>
  if (status === "error") return <Badge variant="destructive" dot>{t("agents.status.error")}</Badge>
  return <Badge variant="neutral">{t("agents.status.disabled")}</Badge>
}

/* ------------------------------------------------------------------ */
/*  Runtime cell                                                       */
/* ------------------------------------------------------------------ */

/**
 * Maps `runtimes.liveness` + `runtime_id` presence onto a 3-state pill.
 * Returns null when there's no runtime (e.g. managed sandbox provider).
 */
type LivenessTone = "online" | "offline" | "pending"

function runtimeLivenessTone(agent: Agent): LivenessTone | null {
  if (!agent.runtime_id) return null
  const lv = (agent.runtime_liveness ?? "").toLowerCase()
  if (lv === "online" || lv === "live") return "online"
  if (lv === "pending_pairing" || lv === "pending") return "pending"
  return "offline"
}

/**
 * Intended execution placement. Prefers the per-agent `runtime` mirror,
 * falls back to `pa.config.daemon_mode` (the backend zeroes the mirror
 * for `agent_daemon` rows). "unknown" only for pre-mirror legacy rows.
 */
function executionPlacement(agent: Agent): "local" | "sandbox" | "unknown" {
  if (agent.runtime === "local") return "local"
  if (agent.runtime === "sandbox") return "sandbox"
  const pa = (agent.config as Record<string, unknown> | undefined) ?? {}
  const mode = String(pa.daemon_mode ?? "")
  if (mode === "sandbox") return "sandbox"
  if (mode === "local") return "local"
  return "unknown"
}

function StatusDot({ tone, title }: { tone: LivenessTone; title?: string }) {
  const color = tone === "online"
    ? "bg-success"
    : tone === "pending"
      ? "bg-warning"
      : "bg-surface-muted"
  return (
    <span
      className={`inline-block h-1.5 w-1.5 rounded-full ${color}`}
      title={title}
      aria-hidden="true"
    />
  )
}

// Hover the truncated name to see the full string. Same tooltip styling as
// ManagedBadge so the popovers feel consistent across the admin pages.
function TruncatedName({
  display,
  full,
  className,
  maxWidthClass = "max-w-[160px]",
}: {
  display: string
  full?: string
  className?: string
  maxWidthClass?: string
}) {
  const tip = full ?? display
  return (
    <Tooltip.Provider delayDuration={150}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>
          <span className={`${maxWidthClass} cursor-help truncate ${className ?? ""}`}>
            {display}
          </span>
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content
            side="top"
            className="z-50 max-w-sm break-all rounded-md border border-line bg-surface px-3 py-2 text-sm leading-relaxed text-fg-muted shadow-lg"
          >
            {tip}
            <Tooltip.Arrow className="fill-white" />
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  )
}

/**
 * "{kind} · {label}" cell. Sandbox dot follows
 * `sandboxes.lifecycle_status` (running → green; spawning/renewing →
 * amber); Local dot follows `runtimes.liveness`.
 */
function RuntimeCell({ agent }: { agent: Agent }) {
  const { t } = useTranslation("admin")
  const placement = executionPlacement(agent)

  // Prefer the active sandbox's E2B id over runtime_name (which is the
  // synthetic "sandbox <pa-shortid>" — meaningless to the user).
  if (placement === "sandbox") {
    const fullId = (agent.sandbox_external_id ?? "").trim()
    if (fullId) {
      const status = (agent.sandbox_status ?? "").toLowerCase()
      const tone: LivenessTone =
        status === "running"
          ? "online"
          : status === "spawning" || status === "renewing"
            ? "pending"
            : "offline"
      return (
        <span className="inline-flex items-center gap-1.5 text-sm text-fg-muted">
          <span className="font-medium text-fg-subtle">Sandbox</span>
          <span className="text-fg-faint">·</span>
          <TruncatedName
            display={fullId}
            className="font-mono text-fg-muted"
            maxWidthClass="max-w-[260px]"
          />
          <StatusDot tone={tone} title={status || undefined} />
        </span>
      )
    }
    // Sandbox placement, no live binding (not yet dispatched, or reaped).
    // Neutral placeholder so users don't mistake it for misconfiguration.
    return (
      <span className="inline-flex items-center gap-1.5 text-sm text-fg-subtle">
        <span className="font-medium">Sandbox</span>
        <span className="text-fg-faint">·</span>
        <span>{t("agents.runtimeCell.pending")}</span>
        <StatusDot tone="offline" />
      </span>
    )
  }

  // Local placement: device name + liveness dot.
  const name = (agent.runtime_name ?? "").trim()
  const runtimeID = (agent.runtime_id ?? "").trim()
  if (placement === "local" && runtimeID && name) {
    const tone = runtimeLivenessTone(agent) ?? "offline"
    return (
      <span className="inline-flex items-center gap-1.5 text-sm text-fg-muted">
        <span className="font-medium text-fg-subtle">Local</span>
        <span className="text-fg-faint">·</span>
        <TruncatedName display={name} />
        <StatusDot tone={tone} title={agent.runtime_liveness || undefined} />
      </span>
    )
  }

  // No runtime row → dispatch is blocked until the user binds one.
  return (
    <Badge variant="warning" dot className="font-normal">
      {t("agents.runtimeCell.unbound")}
    </Badge>
  )
}

/* ------------------------------------------------------------------ */
/*  List page                                                          */
/* ------------------------------------------------------------------ */

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
  // Spinning row icon + double-click guard for the Chat button.
  const [chatPendingID, setChatPendingID] = useState<string | null>(null)

  const query = useAgents(wid)
  const createMut = useCreateAgent(wid)
  const cloneMut = useCreateAgent(wid)
  const modelsQ = useModels(wid)
  const updateMut = useUpdateAgent(wid)
  const updateProfileMut = useUpdateAgentProfile(wid)
  const statusMut = useSetAgentStatus(wid)
  const workspacesQ = useMyWorkspaces()
  const marketplaceQ = useMarketplaceList(wid)
  const agents = useMemo(() => query.data?.agents ?? [], [query.data])
  const workspaceRole = workspacesQ.data?.workspaces.find((w) => w.id === wid)?.role
  const pendingCapabilityID = new URLSearchParams(window.location.search).get("pendingCapability")
  const pendingCapability = (marketplaceQ.data ?? []).find((capability) => capability.id === pendingCapabilityID)

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  const filtered = agents.filter((a) => {
    if (!keyword) return true
    const q = keyword.toLowerCase()
    return a.name.toLowerCase().includes(q) || a.description.toLowerCase().includes(q)
  })

  // Spawns a fresh conversation rather than grafting onto unrelated
  // history; mirrors the post-create flow.
  async function startChatWith(a: Agent) {
    if (!wid || chatPendingID) return
    setChatPendingID(a.id)
    try {
      const conversation = await createConversation(wid, {
        title: starterConversationTitle(a.name, i18n.language),
        surface: "web",
        form: "thread",
        agent_id: a.id,
      })
      navigate("conversations", { id: conversation.id, focus: "compose" })
    } catch {
      // Fall back to the detail page on failure — recoverable surface vs.
      // silent dead click.
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
      {pendingCapabilityID && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-info-border bg-info-subtle px-3 py-2 text-sm text-info-emphasis">
          <span>{pendingCapability ? t("agents.pendingCapability.banner", { name: pendingCapability.name, source: pendingCapability.source_workspace_name ?? "—" }) : t("agents.pendingCapability.loading")}</span>
          <Button variant="outline" size="sm" onClick={() => navigate("agents", { pendingCapability: null })}>{t("agents.pendingCapability.cancel")}</Button>
        </div>
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
        <div className="space-y-4">
          <div className="flex items-center justify-end gap-3">
            <div className="relative w-72">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" strokeWidth={1.75} />
              <Input
                placeholder={t("agents.search.placeholder")}
                className="pl-8 text-xs"
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
              />
            </div>
          </div>

          {filtered.length === 0 ? (
            <EmptyState
              icon={Bot}
              title={t("agents.emptyFiltered.title")}
              description={t("agents.emptyFiltered.description")}
            />
          ) : (
            <div className="overflow-hidden rounded-lg border border-line bg-surface">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("agents.table.agent")}</TableHead>
                    <TableHead>{t("agents.table.runtime")}</TableHead>
                    <TableHead>{t("agents.table.model")}</TableHead>
                    <TableHead>{t("agents.table.visibility")}</TableHead>
                    <TableHead>{t("agents.table.creator")}</TableHead>
                    <TableHead className="text-right pr-4">{t("agents.table.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((a) => (
                    <TableRow
                      key={a.id}
                      className="cursor-pointer"
                      onClick={() => navigate("agents", { id: a.id })}
                    >
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Bot className="h-3.5 w-3.5 shrink-0 text-fg-faint" strokeWidth={1.75} />
                          <span className="text-base font-medium text-fg">{a.name}</span>
                        </div>
                      </TableCell>
                      <TableCell><RuntimeCell agent={a} /></TableCell>
                      <TableCell className="font-mono text-sm text-fg-muted">{defaultModelOf(a, modelsQ.data?.models ?? [])}</TableCell>
                      <TableCell className="text-sm text-fg-subtle">
                        {t(`agents.visibility.${a.visibility ?? "workspace"}`)}
                      </TableCell>
                      <TableCell className="text-sm text-fg-muted">{a.created_by_name || "—"}</TableCell>
                      <TableCell className="pr-4">
                        <RowActions>
                          <ActionIconButton
                            icon={MessageSquare}
                            label={t("agents.actions.chat")}
                            tone="primary"
                            disabled={a.status !== "active" || !wid}
                            busy={chatPendingID === a.id}
                            onClick={() => void startChatWith(a)}
                          />
                          <ActionIconButton
                            icon={Pencil}
                            label={t("agents.actions.edit")}
                            onClick={() => setEditAgent(a)}
                          />
                          <ActionIconButton
                            icon={Copy}
                            label={t("agents.actions.clone")}
                            onClick={() => setCloneAgent(a)}
                          />
                          <ActionIconButton
                            icon={Trash2}
                            label={t("agents.actions.delete")}
                            tone="danger"
                            disabled={a.status !== "active"}
                            onClick={() => setDeleteTarget(a)}
                          />
                        </RowActions>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      )}

      <CreateAgentDialog
        open={createOpen}
        mode="create"
        workspaceID={wid}
        workspaceRole={workspaceRole}
        models={modelsQ.data?.models ?? []}
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
                  const conversation = await createConversation(wid, {
                    title: starterConversationTitle(created.name, i18n.language),
                    surface: "web",
                    form: "thread",
                    agent_id: created.id,
                  })
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
        workspaceRole={workspaceRole}
        models={modelsQ.data?.models ?? []}
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

      {/* Clone reuses the create dialog with the source agent prefilled. */}
      <CreateAgentDialog
        open={cloneAgent !== null}
        mode="create"
        workspaceID={wid}
        workspaceRole={workspaceRole}
        models={modelsQ.data?.models ?? []}
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

      <AlertDialog open={deleteTarget !== null} onOpenChange={(open) => {
        if (!open && !statusMut.isPending) setDeleteTarget(null)
      }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("agents.listActions.deleteConfirmTitle", { name: deleteTarget?.name ?? "" })}</AlertDialogTitle>
            <AlertDialogDescription>{t("agents.listActions.deleteConfirmDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" size="sm" disabled={statusMut.isPending}>{t("agents.listActions.cancel")}</Button>
            </AlertDialogCancel>
            <Button
              variant="destructive"
              size="sm"
              disabled={!deleteTarget || statusMut.isPending}
              onClick={() => {
                if (!deleteTarget) return
                statusMut.mutate(
                  { agentID: deleteTarget.id, enabled: false },
                  {
                    onSuccess: () => {
                      setToast(t("agents.listActions.deletedToast", { name: deleteTarget.name }))
                      setDeleteTarget(null)
                    },
                  },
                )
              }}
            >
              {statusMut.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              {t("agents.listActions.deleteConfirm")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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

/* ------------------------------------------------------------------ */
/*  Agent Detail (7 tabs)                                              */
/* ------------------------------------------------------------------ */

export function AgentDetailPage({ id }: { id: string }) {
  const { t } = useTranslation("admin")
  const { navigate, tab: requestedTab } = useAdminView()
  const wid = useWorkspaceId()
  const [toast, setToast] = useState<string | null>(null)
  const pendingCapabilityID = new URLSearchParams(window.location.search).get("pendingCapability")

  // Detail reads from the cached list; no per-agent endpoint surfaced
  // in admin scope yet.
  const query = useAgents(wid)
  const modelsQ = useModels(wid)
  const workspacesQ = useMyWorkspaces()
  const updateMut = useUpdateAgent(wid)
  const statusMut = useSetAgentStatus(wid)
  const agents = query.data?.agents ?? []
  const agent = agents.find((a) => a.id === id) ?? agents[0]
  const workspaceRole = workspacesQ.data?.workspaces.find((w) => w.id === wid)?.role

  const agentCapabilitiesQ = useAgentCapabilitiesQuery(wid, agent?.id ?? null)
  const workspaceCapabilitiesQ = useCapabilitiesQuery(wid)
  const marketplaceQ = useMarketplaceList(wid)

  if (query.isLoading) {
    return (
      <AdminLayout activeMenu="agents">
        <AgentsLoadingSkeleton />
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

  const model = defaultModelOf(agent, modelsQ.data?.models ?? [])
  const connector = connectorLabel(agent.connector_type)

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
        action={<AgentStatusBadge status={agent.status} />}
      />

      {toast && (
        <div className="mb-4 rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
          {toast}
        </div>
      )}
      {pendingCapabilityID && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md border border-info-border bg-info-subtle px-3 py-2 text-sm text-info-emphasis">
          <span>{t("agents.pendingCapability.detailBanner", { name: (marketplaceQ.data ?? []).find((capability) => capability.id === pendingCapabilityID)?.name ?? pendingCapabilityID, source: (marketplaceQ.data ?? []).find((capability) => capability.id === pendingCapabilityID)?.source_workspace_name ?? "—" })}</span>
          <Button variant="outline" size="sm" onClick={() => navigate("agents", { id: agent.id, tab: "config", pendingCapability: null })}>{t("agents.pendingCapability.cancel")}</Button>
        </div>
      )}

      <Tabs defaultValue={requestedTab ?? "dynamics"}>
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
            connectorLabel={connector}
            installedCapabilities={agentCapabilitiesQ.data?.installed ?? []}
            availableCapabilities={agentCapabilitiesQ.data?.available ?? workspaceCapabilitiesQ.data?.capabilities ?? []}
            capabilitiesLoading={agentCapabilitiesQ.isLoading || workspaceCapabilitiesQ.isLoading}
            capabilitiesError={agentCapabilitiesQ.error ?? workspaceCapabilitiesQ.error}
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

function Field({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="mb-2 last:mb-0">
      <dt className="mb-0.5 text-xs uppercase tracking-wider text-fg-faint">{label}</dt>
      <dd className={`text-sm text-fg-emphasis ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  )
}

function TagsField({ label, tags }: { label: string; tags: string[] }) {
  return (
    <div className="mb-2 last:mb-0">
      <dt className="mb-1 text-xs uppercase tracking-wider text-fg-faint">{label}</dt>
      <dd className="flex flex-wrap gap-1.5">
        {tags.length === 0 ? (
          <span className="text-sm text-fg-faint">—</span>
        ) : (
          tags.map((tag) => <Badge key={tag} variant="neutral">{tag}</Badge>)
        )}
      </dd>
    </div>
  )
}

function capabilityFromBinding(binding: AgentCapability, workspaceID: string | null): Capability | undefined {
  if (!binding.capability_id || !binding.capability_version_id) return undefined
  return {
    id: binding.capability_id,
    workspace_id: binding.workspace_id ?? workspaceID ?? "",
    type: binding.type ?? "mcp",
    name: binding.name ?? tCapabilityFallback(binding.capability_id),
    description: binding.description ?? "",
    visibility: binding.visibility,
    status: binding.status ?? "active",
    required_credentials: binding.required_credentials,
    deprecated_at: binding.deprecated_at,
    from_marketplace: !!binding.workspace_id && binding.workspace_id !== workspaceID,
    source_workspace_id: binding.workspace_id,
    source_workspace_name: binding.source_workspace_name,
    latest_version_id: binding.latest_version_id,
    latest_version: binding.latest_version,
    latest_version_created_at: binding.latest_version_created_at,
    pinned_version_id: binding.capability_version_id,
    pinned_version: binding.version,
    creator_id: "",
    created_at: binding.latest_version_created_at ?? new Date().toISOString(),
    updated_at: binding.latest_version_created_at ?? new Date().toISOString(),
  }
}

function tCapabilityFallback(capabilityID: string) {
  return `Capability ${capabilityID.slice(0, 8)}`
}

// BuiltinCapabilityCard renders a runtime-injected built-in (e.g. chat-history)
// as a default-ON card with a single on/off toggle. Built-ins have no
// capability_version, so this deliberately skips the version-query machinery in
// CapabilityCard; toggling off writes the per-agent disable flag which the
// connector reads at prompt time to suppress injection.
function BuiltinCapabilityCard({
  binding,
  agent,
  workspaceID,
  isAdmin,
  onToast,
}: {
  binding: AgentCapability
  agent: Agent
  workspaceID: string | null
  isAdmin: boolean
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const capability = binding.capability
  const key = binding.builtin_key ?? capability?.builtin_key ?? ""
  const mut = useToggleBuiltinCapabilityMutation(workspaceID, agent.id)
  const enabled = binding.enabled
  const onToggle = (next: boolean) => {
    if (!key || mut.isPending) return
    mut.mutate(
      { key, enabled: next },
      { onError: (e) => onToast(t("agents.detail.capabilities.builtin.toggleError", { message: e instanceof Error ? e.message : String(e) })) },
    )
  }
  return (
    <div className="rounded-md border border-line p-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability?.name ?? key}</span>
            {capability?.type && <CapabilityTypeBadge type={capability.type} />}
            <Badge variant="neutral">{t("agents.detail.capabilities.builtin.badge")}</Badge>
          </div>
          {capability?.description && <p className="mt-1 text-sm text-fg-subtle">{capability.description}</p>}
        </div>
        <label className={`flex shrink-0 items-center gap-2 text-sm ${isAdmin ? "cursor-pointer" : "cursor-not-allowed opacity-60"} text-fg-subtle`}>
          <input
            type="checkbox"
            className="h-4 w-4"
            checked={enabled}
            disabled={!isAdmin || mut.isPending}
            onChange={(e) => onToggle(e.target.checked)}
          />
          <span>{enabled ? t("agents.detail.capabilities.builtin.on") : t("agents.detail.capabilities.builtin.off")}</span>
        </label>
      </div>
    </div>
  )
}

function CapabilityCard({
  item,
  agent,
  workspaceID,
  credentials,
  language,
  mode,
  onToast,
}: {
  item: { capability?: Capability; binding?: AgentCapability }
  agent: Agent
  workspaceID: string | null
  credentials: UserCredential[]
  language: string
  mode: "enabled" | "available"
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const capability = item.capability
  const binding = item.binding
  const missingCredentialKinds = (capability?.required_credentials ?? [])
    .filter((rc) => rc.required && !credentials.some((cred) => cred.kind === rc.kind))
    .map((rc) => rc.kind)
  const versionsQ = useCapabilityVersionsQuery(workspaceID, capability?.id ?? null)
  const versions = versionsQ.data?.versions ?? []
  const boundVersion = versions.find((version) => version.id === binding?.capability_version_id) ?? (binding?.capability_version_id && capability?.pinned_version ? { id: binding.capability_version_id, capability_id: capability.id, version: capability.pinned_version, created_at: capability.latest_version_created_at ?? capability.created_at } as CapabilityVersion : undefined)
  const marketplaceLatest = capability?.latest_version_id ? { id: capability.latest_version_id, capability_id: capability.id, version: capability.latest_version ?? capability.latest_published_version ?? "—", created_at: capability.latest_version_created_at ?? capability.created_at ?? new Date().toISOString() } as CapabilityVersion : undefined
  const latest = latestVersion(versions) ?? marketplaceLatest
  const versionDeleted = !!binding && !versionsQ.isLoading && !boundVersion && !capability?.latest_version_id
  const missingCredential = missingCredentialKinds.length > 0
  const fromMarketplace = !!capability?.from_marketplace || (!!capability?.source_workspace_id && capability.source_workspace_id !== workspaceID)
  const deprecated = !!capability?.deprecated_at
  const border = mode === "available" ? "border-dashed border-line-strong" : "border-line"

  if (!capability && binding) {
    return (
      <div className="rounded-md border border-warning-border bg-warning-subtle/60 p-3">
        <p className="text-sm font-medium text-warning-emphasis">{t("agents.detail.capabilities.deletedCapability.title")}</p>
        <p className="mt-1 text-sm text-warning-emphasis">{t("agents.detail.capabilities.deletedCapability.description")}</p>
        <RemoveCapabilityDialog
          agent={agent}
          binding={binding}
          capabilityName={t("agents.detail.capabilities.deletedCapability.fallbackName")}
          workspaceID={workspaceID}
          onToast={onToast}
        />
      </div>
    )
  }
  if (!capability) return null

  return (
    <div className={`rounded-md border ${border} p-3`}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability.name}</span>
            <CapabilityTypeBadge type={capability.type} />
            {fromMarketplace && <Badge variant="primary">{t("agents.detail.capabilities.marketplace.badge")}</Badge>}
            {missingCredential && <Badge variant="destructive">{t("agents.detail.capabilities.credential.missingBadge")}</Badge>}
            {versionDeleted && <Badge variant="destructive">{t("agents.detail.capabilities.bindings.versionDeleted.warning")}</Badge>}
            {versionDeleted && versions.length > 0 && binding && (
              <SwitchVersionDialog
                agent={agent}
                capability={capability}
                binding={binding}
                workspaceID={workspaceID}
                triggerLabel={t("agents.detail.capabilities.bindings.versionDeleted.switchAction")}
                triggerVariant="link"
                onToast={onToast}
              />
            )}
          </div>
          {capability.description && <p className="mt-1 text-sm text-fg-subtle">{capability.description}</p>}
          {fromMarketplace && <p className="mt-1 text-sm text-fg-subtle">{t("agents.detail.capabilities.marketplace.source", { source: capability.source_workspace_name ?? "—", version: boundVersion?.version ?? capability.pinned_version ?? latest?.version ?? "—" })}</p>}
        </div>
      </div>

      {mode === "enabled" && deprecated && (
        <div className="mt-3 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm leading-5 text-danger-emphasis">
          {t("agents.detail.capabilities.marketplace.deprecatedBanner", { version: boundVersion?.version ?? capability.pinned_version ?? "—" })}
        </div>
      )}

      {mode === "enabled" && fromMarketplace && binding && latest && latest.id !== binding.capability_version_id && (
        <UpgradeCapabilityDialog
          agent={agent}
          capability={capability}
          binding={binding}
          latestVersion={latest}
          workspaceID={workspaceID}
          disabled={deprecated}
          onToast={onToast}
        />
      )}

      <CredentialStatus capability={capability} credentials={credentials} language={language} />

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          {binding ? (
            <Badge variant="neutral" className="font-mono">{boundVersion ? `v${boundVersion.version}` : "v—"}</Badge>
          ) : latest ? (
            <Badge variant="neutral" className="font-mono">v{latest.version} · {t("agents.detail.capabilities.switchDialog.latest")}</Badge>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {mode === "available" ? (
            <EnableCapabilityDialog
              agent={agent}
              capability={capability}
              workspaceID={workspaceID}
              credentials={credentials}
              language={language}
              onToast={onToast}
            />
          ) : binding ? (
            <>
              {versions.length > 1 && !versionDeleted && !fromMarketplace && (
                <SwitchVersionDialog
                  agent={agent}
                  capability={capability}
                  binding={binding}
                  workspaceID={workspaceID}
                  onToast={onToast}
                />
              )}
              <RemoveCapabilityDialog
                agent={agent}
                binding={binding}
                capabilityName={capability.name}
                workspaceID={workspaceID}
                onToast={onToast}
              />
            </>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function CredentialStatus({ capability, credentials, language }: { capability: Capability; credentials: UserCredential[]; language: string }) {
  const { t } = useTranslation("admin")
  const requiredCreds = capability.required_credentials ?? []
  if (requiredCreds.length === 0) {
    return <p className="mt-3 text-sm text-fg-subtle">{t("agents.detail.capabilities.credential.none")}</p>
  }
  return (
    <div className="mt-3 space-y-1.5">
      {requiredCreds.map((rc) => {
        const credential = credentials.find((cred) => cred.kind === rc.kind)
        const label = credentialKindLabel(rc.kind, language, rc.kind)
        return (
          <div key={rc.kind} className={`rounded-md border px-3 py-2 text-sm ${credential ? "border-success-border bg-success-subtle text-success-emphasis" : "border-danger-border bg-danger-subtle text-danger-emphasis"}`}>
            {credential ? (
              <span>{t("agents.detail.capabilities.credential.present", { kind: label, name: credential.display_name || t("agents.detail.capabilities.credential.defaultName") })}</span>
            ) : (
              <span>{t("agents.detail.capabilities.credential.missing", { kind: label })}</span>
            )}
            <CredentialLink kind={rc.kind} className="ml-2 font-medium underline underline-offset-2" />
          </div>
        )
      })}
    </div>
  )
}

function CredentialLink({ kind, className, children }: { kind?: string; className?: string; children?: React.ReactNode }) {
  const { t } = useTranslation("admin")
  if (!kind) return null
  return (
    <a className={className ?? "text-sm font-medium text-fg underline underline-offset-2"} href={credentialURL(kind)}>
      {children ?? t("agents.detail.capabilities.credential.addCta")}
    </a>
  )
}

function credentialURL(kind: string) {
  const current = window.location.pathname + window.location.search
  return `?profile=credentials&kind=${encodeURIComponent(kind)}&returnTo=${encodeURIComponent(current)}`
}

function mutationError(error: unknown) {
  return error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
}

function latestVersion(versions: CapabilityVersion[]) {
  return versions[0]
}

function VersionSelect({ versions, value, onChange }: { versions: CapabilityVersion[]; value: string; onChange: (value: string) => void }) {
  const { t } = useTranslation("admin")
  return (
    <select
      value={value}
      onChange={(event) => onChange(event.target.value)}
      className="h-8 w-full rounded-md border border-line bg-surface px-2 text-sm text-fg focus:outline-none focus:ring-2 focus:ring-slate-300"
    >
      {versions.map((version, index) => (
        <option key={version.id} value={version.id}>v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}</option>
      ))}
    </select>
  )
}

/**
 * EnableCredentialStatusList shows a flat ✓/⚠ list of credential kinds
 * the creator needs. Used by the per-capability enable dialog where the
 * full 3-source picker is overkill — the dialog only enables one
 * capability, no agent visibility decision is being made here.
 *
 * onAllReady fires with true iff every required kind has a configured
 * user_credentials row for the current user.
 */
function EnableCredentialStatusList({
  requiredKinds,
  onAllReady,
}: {
  requiredKinds: { kind: string }[]
  onAllReady: (ready: boolean) => void
}) {
  const { t } = useTranslation("admin")
  const credentialsQ = useMyCredentials()
  const credentials = credentialsQ.data?.credentials ?? []
  const allReady = requiredKinds.every((rc) => credentials.some((c) => c.kind === rc.kind))
  useEffect(() => {
    onAllReady(allReady)
  }, [allReady, onAllReady])
  return (
    <div className="space-y-1.5">
      {requiredKinds.map((rc) => {
        const has = credentials.some((c) => c.kind === rc.kind)
        return (
          <div
            key={rc.kind}
            className={
              has
                ? "flex items-center gap-2 rounded-md border border-success-border bg-success-subtle px-3 py-1.5 text-sm text-success-emphasis"
                : "flex items-center gap-2 rounded-md border border-warning-border bg-warning-subtle px-3 py-1.5 text-sm text-warning-emphasis"
            }
          >
            <span>{has ? "✓" : "⚠"}</span>
            <span>{rc.kind}</span>
            {!has && <span className="ml-auto text-xs">{t("credentialCheck.personalYouMissing")}</span>}
          </div>
        )
      })}
    </div>
  )
}

function EnableCapabilityDialog({
  agent,
  capability,
  workspaceID,
  credentials,
  language,
  onToast,
}: {
  agent: Agent
  capability: Capability
  workspaceID: string | null
  credentials: UserCredential[]
  language: string
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState("")
  const [allCredentialsSatisfied, setAllCredentialsSatisfied] = useState(true)
  const versionsQ = useCapabilityVersionsQuery(workspaceID, open ? capability.id : null)
  const mut = useEnableAgentCapabilityMutation(workspaceID, agent.id)
  const versions = versionsQ.data?.versions ?? []
  const requiredCreds = capability.required_credentials ?? []
  const requiredKinds = requiredCreds.filter((rc) => rc.required)
  const marketplaceFallbackVersion = capability.latest_version_id ? { id: capability.latest_version_id, capability_id: capability.id, version: capability.latest_version ?? "—", created_at: capability.latest_version_created_at ?? capability.created_at } as CapabilityVersion : undefined
  const selectedVersion = versions.find((version) => version.id === selected) ?? latestVersion(versions) ?? marketplaceFallbackVersion
  const canEnable = !!selectedVersion && (requiredKinds.length === 0 || allCredentialsSatisfied) && !mut.isPending

  void credentials
  void language

  useEffect(() => {
    if (!open) return
    setAllCredentialsSatisfied(requiredKinds.length === 0)
  }, [open, requiredKinds.length])

  useEffect(() => {
    if (open && versions.length > 0 && !selected) setSelected(versions[0].id)
    if (open && versions.length === 0 && marketplaceFallbackVersion && !selected) setSelected(marketplaceFallbackVersion.id)
  }, [open, selected, versions, marketplaceFallbackVersion])

  const submit = () => {
    if (!selectedVersion) return
    mut.mutate({ capabilityVersionID: selectedVersion.id }, {
      onSuccess: () => {
        setOpen(false)
        onToast(t("agents.detail.capabilities.toast.enabled", { cap: capability.name, agent: agent.name, version: selectedVersion.version }))
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant="default" size="sm" onClick={() => setOpen(true)}>{t("agents.detail.capabilities.actions.enable")}</Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("agents.detail.capabilities.enableDialog.title", { agent: agent.name, cap: capability.name })}</DialogTitle>
          <DialogDescription>{t("agents.detail.capabilities.enableDialog.description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid gap-1.5">
            <label className="text-sm font-medium text-fg-muted">{t("agents.detail.capabilities.enableDialog.version")}</label>
            {versionsQ.isLoading ? <Skeleton className="h-8 w-full" /> : <VersionSelect versions={versions} value={selectedVersion?.id ?? ""} onChange={setSelected} />}
          </div>
          {requiredKinds.length > 0 ? (
            <EnableCredentialStatusList
              requiredKinds={requiredKinds}
              onAllReady={setAllCredentialsSatisfied}
            />
          ) : (
            <div className="rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
              {t("agents.detail.capabilities.enableDialog.noCredential")}
            </div>
          )}
          {mutationError(mut.error) && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{mutationError(mut.error)}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => setOpen(false)} disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button>
          <Button size="sm" disabled={!canEnable} onClick={submit}>{mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("agents.detail.capabilities.actions.enableConfirm")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SwitchVersionDialog({
  agent,
  capability,
  binding,
  workspaceID,
  triggerLabel,
  triggerVariant = "ghost",
  onToast,
}: {
  agent: Agent
  capability: Capability
  binding: AgentCapability
  workspaceID: string | null
  triggerLabel?: string
  triggerVariant?: "ghost" | "link"
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState(binding.capability_version_id)
  const versionsQ = useCapabilityVersionsQuery(workspaceID, open ? capability.id : null)
  const mut = useEnableAgentCapabilityMutation(workspaceID, agent.id)
  const versions = versionsQ.data?.versions ?? []
  const selectedVersion = versions.find((version) => version.id === selected)
  const canSwitch = !!selectedVersion && selected !== binding.capability_version_id && !mut.isPending

  useEffect(() => {
    if (!open || versions.length === 0) return
    if (!versions.some((version) => version.id === selected)) setSelected(versions[0].id)
  }, [open, selected, versions])

  const submit = () => {
    if (!selectedVersion) return
    mut.mutate({ capabilityVersionID: selectedVersion.id }, {
      onSuccess: () => {
        setOpen(false)
        onToast(t("agents.detail.capabilities.toast.switched", { cap: capability.name, version: selectedVersion.version }))
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant={triggerVariant} size="sm" className={triggerVariant === "link" ? "h-auto px-1 py-0 text-sm text-danger-emphasis" : undefined} onClick={() => setOpen(true)}>{triggerLabel ?? t("agents.detail.capabilities.actions.switchVersion")}</Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("agents.detail.capabilities.switchDialog.title", { cap: capability.name })}</DialogTitle>
          <DialogDescription>{t("agents.detail.capabilities.switchDialog.description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          {versionsQ.isLoading ? <Skeleton className="h-28 w-full" /> : versions.map((version, index) => (
            <label key={version.id} className={`flex cursor-pointer items-start gap-3 rounded-md border p-3 ${selected === version.id ? "border-line-strong bg-surface-subtle" : "border-line"}`}>
              <input type="radio" name="capability-version" className="mt-1" checked={selected === version.id} onChange={() => setSelected(version.id)} />
              <span className="flex-1 text-sm text-fg-emphasis">v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}{version.id === binding.capability_version_id ? ` · ${t("agents.detail.capabilities.switchDialog.current")}` : ""}</span>
            </label>
          ))}
          <p className="rounded-md border border-info-border bg-info-subtle px-3 py-2 text-sm text-info-emphasis">{t("agents.detail.capabilities.switchDialog.notice", { agent: agent.name })}</p>
          {mutationError(mut.error) && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{mutationError(mut.error)}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => setOpen(false)} disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button>
          <Button size="sm" disabled={!canSwitch} onClick={submit}>{mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{selectedVersion ? t("agents.detail.capabilities.actions.switchConfirm", { version: selectedVersion.version }) : t("agents.detail.capabilities.actions.switchVersion")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RemoveCapabilityDialog({
  agent,
  binding,
  capabilityName,
  workspaceID,
  onToast,
}: {
  agent: Agent
  binding: AgentCapability
  capabilityName: string
  workspaceID: string | null
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const mut = useDeleteAgentCapabilityMutation(workspaceID, agent.id)
  const submit = () => {
    mut.mutate(binding.capability_version_id, {
      onSuccess: () => {
        setOpen(false)
        onToast(t("agents.detail.capabilities.toast.removed", { cap: capabilityName, agent: agent.name }))
      },
    })
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>{t("agents.detail.capabilities.actions.remove")}</Button>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("agents.detail.capabilities.removeDialog.title", { agent: agent.name, cap: capabilityName })}</AlertDialogTitle>
          <AlertDialogDescription>{t("agents.detail.capabilities.removeDialog.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="space-y-1 rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm text-fg-muted">
          <li>{t("agents.detail.capabilities.removeDialog.impactRun")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCapability")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCredential")}</li>
        </ul>
        {mutationError(mut.error) && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{mutationError(mut.error)}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel asChild><Button variant="outline" size="sm" disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button></AlertDialogCancel>
          <Button variant="destructive" size="sm" disabled={mut.isPending} onClick={submit}>{mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("agents.detail.capabilities.actions.removeConfirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function Card({ title, className, children }: { title: string; className?: string; children: React.ReactNode }) {
  return (
    <section className={`rounded-lg border border-line bg-surface p-4 ${className ?? ""}`}>
      <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>
      {children}
    </section>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="text-xs uppercase tracking-wider text-fg-faint">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums text-fg">{value}</div>
    </div>
  )
}

/**
 * Agent visibility radio + confirmation dialog when tightening FROM
 * `public` to a stricter tier. RBAC-gated client-side; backend re-enforces.
 */
function VisibilityCard({
  agentID,
  workspaceID,
  current,
  canEdit,
  onSuccess,
}: {
  agentID: string
  workspaceID: string | null
  current: AgentVisibility
  canEdit: boolean
  onSuccess: (next: AgentVisibility) => void
}) {
  const { t } = useTranslation("admin")
  const mut = useUpdateAgentVisibility(workspaceID)
  const [pendingDowngrade, setPendingDowngrade] = useState<AgentVisibility | null>(null)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const apply = (next: AgentVisibility) => {
    setErrorMsg(null)
    mut.mutate(
      { agentID, visibility: next },
      {
        onSuccess: () => {
          setPendingDowngrade(null)
          onSuccess(next)
        },
        onError: (err) => {
          setErrorMsg((err as Error)?.message ?? t("agents.visibility.updateError"))
        },
      },
    )
  }

  const onSelect = (next: AgentVisibility) => {
    if (next === current) return
    // Tightening from public kicks out external users — confirm first.
    if (current === "public" && next !== "public") {
      setPendingDowngrade(next)
      return
    }
    apply(next)
  }

  const tiers: { value: AgentVisibility; hint: string }[] = [
    { value: "workspace", hint: t("agents.visibility.workspaceHint") },
    { value: "tenant", hint: t("agents.visibility.tenantHint") },
    { value: "public", hint: t("agents.visibility.publicHint") },
  ]

  return (
    <Card title={t("agents.table.visibility")} className="mt-4">
      <div className="space-y-2">
        {tiers.map((tier) => (
          <label
            key={tier.value}
            className={`flex cursor-pointer items-start gap-3 rounded-md border p-3 transition-colors ${
              current === tier.value
                ? "border-line-strong bg-surface-subtle"
                : "border-line hover:bg-surface-subtle"
            } ${!canEdit ? "cursor-not-allowed opacity-60" : ""}`}
          >
            <input
              type="radio"
              name="agent-visibility"
              value={tier.value}
              checked={current === tier.value}
              disabled={!canEdit || mut.isPending}
              onChange={() => onSelect(tier.value)}
              className="mt-1"
            />
            <div className="flex-1">
              <div className="text-sm font-medium text-fg">
                {t(`agents.visibility.${tier.value}`)}
              </div>
              <div className="mt-0.5 text-sm text-fg-subtle">{tier.hint}</div>
            </div>
          </label>
        ))}
      </div>
      {!canEdit && (
        <p className="mt-2 text-sm text-fg-faint">
          {t("agents.visibility.ownerOnly")}
        </p>
      )}
      {errorMsg && (
        <p className="mt-2 text-sm text-danger" role="alert">
          {errorMsg}
        </p>
      )}

      {pendingDowngrade && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-surface-emphasis/40 px-4">
          <div className="w-full max-w-md rounded-lg bg-surface p-5 shadow-xl">
            <h4 className="text-base font-semibold text-fg">
              {t("agents.visibility.downgradeWarnTitle")}
            </h4>
            <p className="mt-2 text-sm text-fg-muted">
              {t("agents.visibility.downgradeWarnBody", {
                to: t(`agents.visibility.${pendingDowngrade}`),
              })}
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setPendingDowngrade(null)}
                className="rounded-md border border-line px-3 py-1.5 text-sm text-fg-muted hover:bg-surface-subtle"
                disabled={mut.isPending}
              >
                {t("agents.visibility.cancel")}
              </button>
              <button
                type="button"
                onClick={() => apply(pendingDowngrade)}
                className="rounded-md bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:opacity-60"
                disabled={mut.isPending}
              >
                {mut.isPending ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  t("agents.visibility.confirmDowngrade")
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </Card>
  )
}

/* ------------------------------------------------------------------ */
/*  AgentDynamicsTab — "动态" tab.                                     */
/* ------------------------------------------------------------------ */

const RECENT_RUNS_LIMIT = 10

function AgentDynamicsTab({ workspaceID, agent }: { workspaceID: string | null; agent: Agent }) {
  const { t } = useTranslation("admin")
  // Filter client-side: list-runs takes only workspace + status, no
  // per-agent query option yet.
  const inflightQ = useAgentRuns(workspaceID, { statuses: ["running", "queued"], limit: 50 })
  const recentQ = useAgentRuns(workspaceID, { limit: 50 })
  const metricsQ = useAgentMetrics(workspaceID, agent.id, 30)

  const inflight = (inflightQ.data?.agent_runs ?? []).filter(
    (r) => r.agent_id === agent.id,
  )
  const recent = (recentQ.data?.agent_runs ?? [])
    .filter((r) => r.agent_id === agent.id)
    .slice(0, RECENT_RUNS_LIMIT)

  return (
    <div className="space-y-4">
      <CurrentWorkCard
        runs={inflight}
        loading={inflightQ.isLoading}
      />
      <MetricsCard
        metrics={metricsQ.data}
        loading={metricsQ.isLoading}
      />
      <RecentRunsCard
        runs={recent}
        loading={recentQ.isLoading}
        showCount={recent.length}
      />
    </div>
  )
}

function CurrentWorkCard({ runs, loading }: { runs: AgentRunSummary[]; loading: boolean }) {
  const { t } = useTranslation("admin")
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.current.title")}
      subtitle={t("agents.detail.dynamics.current.subtitle")}
    >
      {loading ? (
        <Skeleton className="h-5 w-2/3" />
      ) : runs.length === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.current.empty")}</p>
      ) : (
        <ul className="space-y-2">
          {runs.map((run) => (
            <li key={run.id} className="flex items-center justify-between rounded-md border border-line px-3 py-2">
              <div className="flex items-center gap-2 text-sm">
                <RunStatusDot status={run.status} />
                <code className="font-mono text-sm text-fg-muted">{shortRunId(run.id)}</code>
                <span className="text-fg-subtle">·</span>
                <span className="text-fg-muted">{run.agent_name ?? "—"}</span>
              </div>
              <span className="text-sm text-fg-subtle">{run.status}</span>
            </li>
          ))}
        </ul>
      )}
    </DynamicsCard>
  )
}

function MetricsCard({ metrics, loading }: { metrics?: AgentMetrics; loading: boolean }) {
  const { t } = useTranslation("admin")
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.metrics.title")}
      subtitle={t("agents.detail.dynamics.metrics.subtitle")}
    >
      {loading ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-16 w-full" />)}
        </div>
      ) : !metrics || metrics.completed_count + metrics.failed_count === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.metrics.empty")}</p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <MetricStat
            label={t("agents.detail.dynamics.metrics.completed")}
            value={metrics.completed_count.toString()}
          />
          <MetricStat
            label={t("agents.detail.dynamics.metrics.successRate")}
            value={formatPercent(metrics.success_rate)}
          />
          <MetricStat
            label={t("agents.detail.dynamics.metrics.avgDuration")}
            value={formatDurationMs(metrics.avg_duration_ms)}
          />
        </div>
      )}
    </DynamicsCard>
  )
}

function MetricStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-line bg-surface-subtle/40 px-3 py-2">
      <div className="text-xs uppercase tracking-wider text-fg-faint">{label}</div>
      <div className="mt-0.5 text-2xl font-semibold tabular-nums text-fg">{value}</div>
    </div>
  )
}

function RecentRunsCard({
  runs,
  loading,
  showCount,
}: {
  runs: AgentRunSummary[]
  loading: boolean
  showCount: number
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()
  return (
    <DynamicsCard
      title={t("agents.detail.dynamics.recent.title")}
      subtitle={t("agents.detail.dynamics.recent.subtitle", { count: showCount })}
    >
      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 w-full" />)}
        </div>
      ) : runs.length === 0 ? (
        <p className="text-sm text-fg-faint">{t("agents.detail.dynamics.recent.empty")}</p>
      ) : (
        <ul className="space-y-2">
          {runs.map((run) => (
            <li key={run.id}>
              <button
                type="button"
                onClick={() => navigate("runs", { id: run.id })}
                className="flex w-full items-center gap-3 rounded-md border border-line px-3 py-2 text-left hover:bg-surface-subtle"
              >
                <RunStatusDot status={run.status} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 text-sm">
                    <code className="font-mono text-sm text-fg-muted">{shortRunId(run.id)}</code>
                    <span className="truncate text-fg-muted">{run.agent_name ?? t("agents.detail.dynamics.recent.untitled")}</span>
                  </div>
                  <div className="mt-0.5 text-xs text-fg-subtle">
                    {fmtAgo(run.created_at)}
                    {run.started_at && run.finished_at && (
                      <> · {formatDurationMs(durationMs(run.started_at, run.finished_at))}</>
                    )}
                  </div>
                </div>
                <ArrowUpRight className="h-3 w-3 text-fg-faint" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </DynamicsCard>
  )
}

function DynamicsCard({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: React.ReactNode
}) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="mb-3 flex items-baseline gap-2">
        <h3 className="text-base font-semibold text-fg">{title}</h3>
        {subtitle && <span className="text-sm text-fg-subtle">{subtitle}</span>}
      </div>
      {children}
    </section>
  )
}

function RunStatusDot({ status }: { status: AgentRunStatus }) {
  const tone =
    status === "completed" ? "bg-success"
      : status === "running" || status === "queued" ? "bg-info"
      : status === "failed" ? "bg-danger"
      : "bg-surface-muted"
  return <span className={`h-2 w-2 shrink-0 rounded-full ${tone}`} />
}

function shortRunId(id: string): string {
  return id.length <= 8 ? id : id.slice(0, 8)
}

function durationMs(startISO: string, endISO: string): number {
  return Math.max(0, Date.parse(endISO) - Date.parse(startISO))
}

function formatPercent(rate: number): string {
  return `${(rate * 100).toFixed(rate >= 0.995 ? 0 : 1)}%`
}

function formatDurationMs(ms: number): string {
  if (!ms || ms <= 0) return "—"
  if (ms < 1000) return `${Math.round(ms)}ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.round(seconds - minutes * 60)
  return remainder === 0 ? `${minutes}m` : `${minutes}m${remainder}s`
}

/* ------------------------------------------------------------------ */
/*  AgentConfigTab — "配置" tab.                                       */
/* ------------------------------------------------------------------ */

function AgentConfigTab({
  agent,
  workspaceID,
  workspaceRole,
  modelLabel,
  connectorLabel,
  installedCapabilities,
  availableCapabilities,
  capabilitiesLoading,
  capabilitiesError,
  onToast,
}: {
  agent: Agent
  workspaceID: string | null
  workspaceRole?: string
  modelLabel: string
  connectorLabel: string
  installedCapabilities: AgentCapability[]
  availableCapabilities: Capability[]
  capabilitiesLoading: boolean
  capabilitiesError: unknown
  onToast: (message: string) => void
}) {
  const { t, i18n } = useTranslation("admin")
  const credentialsQ = useMyCredentials()
  const credentials = credentialsQ.data?.credentials ?? []
  const installedIDs = new Set(installedCapabilities.map((item) => item.capability_id))
  const enabledCaps = installedCapabilities
    .filter((item) => item.enabled || item.built_in)
    .map((item) => {
      const raw = item as AgentCapability & { capability?: Capability }
      return {
        binding: item,
        capability: raw.capability
          ?? availableCapabilities.find((cap) => cap.id === item.capability_id)
          ?? capabilityFromBinding(item, workspaceID),
      }
    })
  const installable = availableCapabilities.filter((cap) => !installedIDs.has(cap.id))
  const isAdmin = workspaceRole === "owner" || workspaceRole === "admin"

  return (
    <div className="space-y-4">
      <Card title={t("agents.detail.config.runtime.title")}>
        <Field label={t("agents.detail.config.runtime.model")} value={modelLabel} mono />
        <Field
          label={t("agents.detail.config.runtime.runtime")}
          value={
            <Badge variant={runtimeOf(agent) === "sandbox" ? "success" : "neutral"} dot>
              {t(`agents.runtime.${runtimeOf(agent)}`)}
            </Badge>
          }
        />
        <Field
          label={t("agents.detail.config.runtime.connector")}
          value={
            <span>
              {connectorLabel}
              <span className="ml-1 text-sm text-fg-subtle">· {agent.connector_type}</span>
            </span>
          }
        />
      </Card>

      {/* Sandbox panel lives under the runtime card because for      */}
      {/* sandbox-mode agents it IS the runtime surface. Skipped for  */}
      {/* local agents (no sandbox to manage).                        */}
      {runtimeOf(agent) === "sandbox" && (
        <SandboxPanel workspaceID={workspaceID} agentID={agent.id} />
      )}

      <ConfigCapabilitiesSection
        agent={agent}
        workspaceID={workspaceID}
        isAdmin={isAdmin}
        enabledCaps={enabledCaps}
        installable={installable}
        credentials={credentials}
        loading={capabilitiesLoading}
        error={capabilitiesError}
        language={i18n.language}
        onToast={onToast}
      />
    </div>
  )
}

function ConfigCapabilitiesSection({
  agent,
  workspaceID,
  isAdmin,
  enabledCaps,
  installable,
  credentials,
  loading,
  error,
  language,
  onToast,
}: {
  agent: Agent
  workspaceID: string | null
  isAdmin: boolean
  enabledCaps: Array<{ binding: AgentCapability; capability?: Capability }>
  installable: Capability[]
  credentials: UserCredential[]
  loading: boolean
  error: unknown
  language: string
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [addOpen, setAddOpen] = useState(false)

  if (loading) {
    return (
      <Card title={t("agents.detail.config.capabilities.title")}>
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12 w-full" />)}
        </div>
      </Card>
    )
  }
  if (error) {
    return (
      <ErrorState
        title={t("agents.detail.config.capabilities.loadError")}
        description={error instanceof Error ? error.message : undefined}
      />
    )
  }

  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-fg-subtle">
          {t("agents.detail.config.capabilities.title")}
        </h3>
        {isAdmin && installable.length > 0 && (
          <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
            {t("agents.detail.config.capabilities.add")}
          </Button>
        )}
      </div>

      {enabledCaps.length === 0 ? (
        <EmptyState
          icon={Plus}
          title={t("agents.detail.config.capabilities.empty")}
          action={
            isAdmin && installable.length > 0 ? (
              <Button size="sm" onClick={() => setAddOpen(true)}>
                {t("agents.detail.config.capabilities.add")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="space-y-2">
          {enabledCaps.map((item) =>
            item.binding.built_in ? (
              <BuiltinCapabilityCard
                key={item.binding.id ?? item.capability?.id}
                binding={item.binding}
                agent={agent}
                workspaceID={workspaceID}
                isAdmin={isAdmin}
                onToast={onToast}
              />
            ) : (
              <CapabilityCard
                key={item.binding.id ?? item.capability?.id}
                item={item}
                agent={agent}
                workspaceID={workspaceID}
                credentials={credentials}
                language={language}
                mode="enabled"
                onToast={onToast}
              />
            )
          )}
        </div>
      )}

      <AddCapabilityDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        agent={agent}
        workspaceID={workspaceID}
        installable={installable}
        credentials={credentials}
        language={language}
        onToast={onToast}
      />
    </section>
  )
}

/**
 * Searchable picker that delegates to EnableCapabilityDialog on select
 * so the version-pick + credential flow isn't forked.
 */
function AddCapabilityDialog({
  open,
  onOpenChange,
  agent,
  workspaceID,
  installable,
  credentials,
  language,
  onToast,
}: {
  open: boolean
  onOpenChange: (next: boolean) => void
  agent: Agent
  workspaceID: string | null
  installable: Capability[]
  credentials: UserCredential[]
  language: string
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [q, setQ] = useState("")
  const filtered = installable.filter((cap) => {
    if (!q.trim()) return true
    const needle = q.toLowerCase()
    return cap.name.toLowerCase().includes(needle)
      || (cap.description ?? "").toLowerCase().includes(needle)
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("agents.detail.config.capabilities.add")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("agents.detail.config.capabilities.add")}
              className="pl-8"
              autoFocus
            />
          </div>
          <div className="max-h-80 space-y-2 overflow-y-auto">
            {filtered.length === 0 ? (
              <p className="rounded-md border border-dashed border-line px-3 py-6 text-center text-sm text-fg-subtle">
                {t("agents.detail.capabilities.emptyAvailable")}
              </p>
            ) : (
              filtered.map((capability) => (
                <div
                  key={capability.id}
                  className="rounded-md border border-line p-3"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-medium text-fg">{capability.name}</span>
                        <CapabilityTypeBadge type={capability.type} />
                      </div>
                      {capability.description && (
                        <p className="mt-1 text-sm text-fg-subtle">{capability.description}</p>
                      )}
                    </div>
                    <EnableCapabilityDialog
                      agent={agent}
                      capability={capability}
                      workspaceID={workspaceID}
                      credentials={credentials}
                      language={language}
                      onToast={(msg) => {
                        onToast(msg)
                        onOpenChange(false)
                      }}
                    />
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {t("agents.detail.capabilities.actions.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
