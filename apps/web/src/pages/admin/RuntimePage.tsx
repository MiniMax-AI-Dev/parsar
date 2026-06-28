import React, { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Cloud,
  PlugZap,
  Skull,
  Zap,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ConnectivityResultPanel } from "../../components/runtime/ConnectivityResultPanel"
import { RuntimeCredentialCard } from "../../components/runtime/RuntimeCredentialCard"
import { LocalDeviceRuntimesPanel } from "./runtimes/LocalDeviceRuntimesPanel"
import { Badge } from "../../components/ui/badge"
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
import { Skeleton } from "../../components/ui/skeleton"
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
import {
  useRuntimeStatus,
  type ConnectivityResult,
  type RuntimeStatus,
} from "../../lib/api-runtime"
import {
  isSandboxDaemonRuntime,
  supportedAgentKinds,
  useWorkspaceRuntimes,
  type Runtime,
} from "../../lib/api-runtimes"
import {
  killSandboxRequestRaw,
  useSandboxConnectivityTest,
  useWorkspaceSandboxes,
  type SandboxBinding,
  type SandboxStatusKind,
} from "../../lib/api-sandbox"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useNow } from "../../lib/use-now"
import { useWorkspaceId } from "../../lib/workspace"

type RuntimeTab = "sandbox" | "local_device" | "external"
type CloudState = "loading" | "notConfigured" | "ready" | "error" | "unknown"
type SortKey = "last_active" | "created_at" | "agent"

type BadgeVariant = "success" | "warning" | "destructive" | "neutral" | "primary"

function SandboxStatusBadge({ kind, status }: { kind: SandboxStatusKind; status: string }) {
  if (kind === "live") return <Badge variant="success" dot>{status}</Badge>
  if (kind === "transient") return <Badge variant="warning" dot>{status}</Badge>
  return <Badge variant="neutral">{status}</Badge>
}

function relativeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (ms < 0 || Number.isNaN(ms)) return iso
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec}秒前`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 48) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}

function sortBindings(bindings: SandboxBinding[], sortKey: SortKey): SandboxBinding[] {
  const copy = bindings.slice()
  if (sortKey === "agent") {
    copy.sort((a, b) => (a.project_agent_id ?? "").localeCompare(b.project_agent_id ?? ""))
  } else if (sortKey === "created_at") {
    copy.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  } else {
    copy.sort((a, b) => new Date(b.last_active_at).getTime() - new Date(a.last_active_at).getTime())
  }
  return copy
}

function useConnectivityCheckLabel(): (name: string) => string {
  const { t } = useTranslation("admin")
  return (name: string) => {
    switch (name) {
      case "sandbox_connect":
        return t("runtime.connectivity.checks.sandboxConnect")
      case "runtime_ready":
        return t("runtime.connectivity.checks.runtimeReady")
      case "prompt_roundtrip":
        return t("runtime.connectivity.checks.promptRoundtrip")
      case "daemon_paired":
        return t("runtime.connectivity.checks.daemonPaired")
      case "daemon_online":
        return t("runtime.connectivity.checks.daemonOnline")
      default:
        return name
    }
  }
}

export function RuntimePage() {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const workspaceID = useWorkspaceId()
  const statusQuery = useRuntimeStatus(workspaceID)
  const sandboxesQuery = useWorkspaceSandboxes(workspaceID)
  const daemonRuntimesQuery = useWorkspaceRuntimes(workspaceID ?? "", "agent_daemon")
  const workspacesQ = useMyWorkspaces()

  const [tab, setTab] = useState<RuntimeTab>("sandbox")
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sortKey, setSortKey] = useState<SortKey>("last_active")
  const [confirming, setConfirming] = useState(false)
  const [bulkPending, setBulkPending] = useState(false)
  const [bulkErrors, setBulkErrors] = useState<{ sandboxID: string; status: number | string; message: string }[]>([])

  useNow()

  const role = workspacesQ.data?.workspaces.find((w) => w.id === workspaceID)?.role
  const isAdmin = role === "owner" || role === "admin"
  const cloudState = resolveCloudState({
    status: statusQuery.data,
    statusLoading: statusQuery.isLoading,
    statusError: Boolean(statusQuery.error),
  })

  const bindings = useMemo(
    () => sortBindings(sandboxesQuery.data ?? [], sortKey),
    [sandboxesQuery.data, sortKey],
  )
  const activeBindings = sandboxesQuery.error ? [] : bindings
  // Offline rows are stale: runtime row is owned by project_agent
  // (deterministic name), not sandbox lifecycle, so when the sandbox
  // dies the row stays and the heartbeat sweeper just flips it offline.
  // Surfacing them as live daemons would mislead.
  const sandboxDaemonRuntimes = useMemo(
    () =>
      (daemonRuntimesQuery.data ?? [])
        .filter(isSandboxDaemonRuntime)
        .filter((rt) => rt.liveness !== "offline"),
    [daemonRuntimesQuery.data],
  )

  async function performBulkKill() {
    if (selected.size === 0 || !workspaceID) return
    setBulkPending(true)
    setBulkErrors([])
    const toKill = activeBindings.filter((b) => selected.has(b.binding_id) && b.project_agent_id)
    const errors: { sandboxID: string; status: number | string; message: string }[] = []
    for (const b of toKill) {
      try {
        await killSandboxRequestRaw(workspaceID, b.project_agent_id as string)
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : null
        errors.push({
          sandboxID: b.sandbox_id,
          status: apiErr?.envelope.status ?? "?",
          message: err instanceof Error ? err.message : String(err),
        })
      }
    }
    setBulkPending(false)
    setConfirming(false)
    setSelected(new Set())
    if (errors.length > 0) setBulkErrors(errors)
    void sandboxesQuery.refetch()
  }

  function toggleOne(bindingID: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(bindingID)) next.delete(bindingID)
      else next.add(bindingID)
      return next
    })
  }

  function toggleAll() {
    if (selected.size === activeBindings.length) setSelected(new Set())
    else setSelected(new Set(activeBindings.map((b) => b.binding_id)))
  }

  return (
    <AdminLayout activeMenu="runtime">
      <PageHeader title={t("runtime.page.title")} />

      <RuntimeTabs tab={tab} onChange={setTab} />

      {tab === "sandbox" && (
        <CloudSandboxPanel
          workspaceID={workspaceID}
          status={statusQuery.data}
          statusError={Boolean(statusQuery.error)}
          cloudState={cloudState}
          isAdmin={isAdmin}
          bindings={activeBindings}
          sandboxDaemonRuntimes={sandboxDaemonRuntimes}
          sandboxDaemonLoading={daemonRuntimesQuery.isLoading && Boolean(workspaceID)}
          sandboxDaemonError={daemonRuntimesQuery.error}
          listLoading={sandboxesQuery.isLoading}
          listError={sandboxesQuery.error}
          sortKey={sortKey}
          selected={selected}
          bulkPending={bulkPending}
          bulkErrors={bulkErrors}
          onRefresh={() => {
            void statusQuery.refetch()
            void sandboxesQuery.refetch()
            void daemonRuntimesQuery.refetch()
          }}
          onSortChange={setSortKey}
          onToggleOne={toggleOne}
          onToggleAll={toggleAll}
          onOpenDetail={(sandboxID) => navigate("runtime", { id: sandboxID })}
          onClearBulkErrors={() => setBulkErrors([])}
          onConfirmBulkKill={() => setConfirming(true)}
        />
      )}

      {tab === "local_device" && (
        <section className="rounded-lg border border-slate-200 bg-white p-4">
          <LocalDeviceRuntimesPanel />
        </section>
      )}

      {tab === "external" && <ExternalAgentPanel />}

      <ConfirmBulkKillDialog
        open={confirming}
        count={selected.size}
        preview={activeBindings.filter((b) => selected.has(b.binding_id)).slice(0, 5).map((b) => b.sandbox_id)}
        loading={bulkPending}
        onCancel={() => setConfirming(false)}
        onConfirm={() => void performBulkKill()}
      />
    </AdminLayout>
  )
}

function CloudSandboxPanel({
  workspaceID,
  status,
  statusError,
  cloudState,
  isAdmin,
  bindings,
  sandboxDaemonRuntimes,
  sandboxDaemonLoading,
  sandboxDaemonError,
  listLoading,
  listError,
  sortKey,
  selected,
  bulkPending,
  bulkErrors,
  onRefresh,
  onSortChange,
  onToggleOne,
  onToggleAll,
  onOpenDetail,
  onClearBulkErrors,
  onConfirmBulkKill,
}: {
  workspaceID: string | null
  status: RuntimeStatus | undefined
  statusError: boolean
  cloudState: CloudState
  isAdmin: boolean
  bindings: SandboxBinding[]
  sandboxDaemonRuntimes: Runtime[]
  sandboxDaemonLoading: boolean
  sandboxDaemonError: unknown
  listLoading: boolean
  listError: unknown
  sortKey: SortKey
  selected: Set<string>
  bulkPending: boolean
  bulkErrors: { sandboxID: string; status: number | string; message: string }[]
  onRefresh: () => void
  onSortChange: (next: SortKey) => void
  onToggleOne: (bindingID: string) => void
  onToggleAll: () => void
  onOpenDetail: (sandboxID: string) => void
  onClearBulkErrors: () => void
  onConfirmBulkKill: () => void
}) {
  const { t } = useTranslation("admin")
  const stateBody = cloudStateBody(t, cloudState, status)
  const showCredentialControl = cloudState !== "loading" && cloudState !== "unknown" && status?.profile !== "managed"
  const showInstances = !statusError && Boolean(workspaceID)

  return (
    <section className="space-y-4">
      <div className="rounded-lg border border-slate-200 bg-white p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-3">
            <div className="rounded-md border border-slate-200 bg-slate-50 p-2 text-slate-700">
              <Cloud className="h-4 w-4" strokeWidth={1.9} />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-[15px] font-semibold text-slate-900">{t("runtime.providers.sandbox.title")}</h2>
                <CloudStateBadge state={cloudState} />
              </div>
              <p className="mt-1 max-w-3xl text-[13px] leading-relaxed text-slate-600">
                {stateBody}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            {showCredentialControl && (
              <RuntimeCredentialCard workspaceID={workspaceID} isAdmin={isAdmin} variant="inline" />
            )}
          </div>
        </div>

      </div>

      <CloudDaemonRuntimesPanel
        runtimes={sandboxDaemonRuntimes}
        loading={sandboxDaemonLoading}
        error={sandboxDaemonError}
        onRefresh={onRefresh}
      />

      {showInstances ? (
        <CloudInstancesPanel
          workspaceID={workspaceID}
          isAdmin={isAdmin}
          bindings={bindings}
          loading={listLoading}
          error={listError}
          sortKey={sortKey}
          selected={selected}
          bulkPending={bulkPending}
          bulkErrors={bulkErrors}
          onRefresh={onRefresh}
          onSortChange={onSortChange}
          onToggleOne={onToggleOne}
          onToggleAll={onToggleAll}
          onOpenDetail={onOpenDetail}
          onClearBulkErrors={onClearBulkErrors}
          onConfirmBulkKill={onConfirmBulkKill}
        />
      ) : null}
    </section>
  )
}

function CloudInstancesPanel({
  workspaceID,
  isAdmin,
  bindings,
  loading,
  error,
  sortKey,
  selected,
  bulkPending,
  bulkErrors,
  onRefresh,
  onSortChange,
  onToggleOne,
  onToggleAll,
  onOpenDetail,
  onClearBulkErrors,
  onConfirmBulkKill,
}: {
  workspaceID: string | null
  isAdmin: boolean
  bindings: SandboxBinding[]
  loading: boolean
  error: unknown
  sortKey: SortKey
  selected: Set<string>
  bulkPending: boolean
  bulkErrors: { sandboxID: string; status: number | string; message: string }[]
  onRefresh: () => void
  onSortChange: (next: SortKey) => void
  onToggleOne: (bindingID: string) => void
  onToggleAll: () => void
  onOpenDetail: (sandboxID: string) => void
  onClearBulkErrors: () => void
  onConfirmBulkKill: () => void
}) {
  const { t } = useTranslation("admin")
  const checkLabelFor = useConnectivityCheckLabel()
  const [testingId, setTestingId] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<{ bindingId: string; result: ConnectivityResult } | null>(null)
  const connTest = useSandboxConnectivityTest()

  function handleTestConnection(b: SandboxBinding) {
    if (!workspaceID || !b.project_agent_id) return
    setTestingId(b.binding_id)
    setTestResult(null)
    connTest.mutateAsync({ workspaceID, projectAgentID: b.project_agent_id }).then(
      (result) => {
        setTestResult({ bindingId: b.binding_id, result })
        setTestingId(null)
      },
      () => {
        setTestingId(null)
      },
    )
  }

  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    )
  }

  if (error) {
    return (
      <ErrorState
        title={t("runtime.list.errors.loadFailed")}
        description={error instanceof Error ? error.message : String(error)}
        onRetry={onRefresh}
      />
    )
  }

  return (
    <section className="rounded-lg border border-slate-200 bg-white">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-4 py-3">
        <div>
          <h3 className="text-[14px] font-medium text-slate-900">{t("runtime.cloud.instances.title")}</h3>
          <p className="mt-0.5 text-[12px] text-slate-500">
            {t("runtime.list.summary", { count: bindings.length })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-2 text-[12px] text-slate-500">
            {t("runtime.list.sort.label")}
            <select
              className="rounded border border-slate-200 bg-white px-2 py-1 text-[12px]"
              value={sortKey}
              onChange={(e) => onSortChange(e.target.value as SortKey)}
              data-testid="runtime-sort"
            >
              <option value="last_active">{t("runtime.list.sort.lastActive")}</option>
              <option value="created_at">{t("runtime.list.sort.createdAt")}</option>
              <option value="agent">{t("runtime.list.sort.agent")}</option>
            </select>
          </label>
          <Button
            aria-label={t("runtime.list.actions.bulkKill", { count: selected.size })}
            className="h-8 w-8 rounded-full p-0 text-red-500 hover:bg-red-50 hover:text-red-700 disabled:text-slate-400"
            size="icon"
            title={t("runtime.list.actions.bulkKill", { count: selected.size })}
            variant="ghost"
            disabled={selected.size === 0 || bulkPending}
            onClick={onConfirmBulkKill}
            data-testid="runtime-bulk-kill"
          >
            <Skull className="h-3.5 w-3.5" strokeWidth={2} />
          </Button>
        </div>
      </div>

      {bulkErrors.length > 0 && (
        <div className="mx-4 mt-3 rounded-md border border-red-200 bg-red-50/70 p-3">
          <div className="mb-2 flex items-center justify-between gap-2">
            <span className="text-[12px] font-medium text-red-900">
              {t("runtime.list.errors.bulkKillPartial")} ({bulkErrors.length})
            </span>
            <button
              type="button"
              onClick={onClearBulkErrors}
              className="text-[11px] text-red-700 hover:underline"
              data-testid="runtime-bulk-error-dismiss"
            >
              {t("runtime.list.errors.bulkKillDismiss")}
            </button>
          </div>
          <ul className="max-h-40 space-y-1 overflow-y-auto text-[11px] text-red-800">
            {bulkErrors.map((e) => (
              <li key={e.sandboxID} className="flex items-start gap-2 font-mono">
                <span className="shrink-0 rounded bg-red-200/70 px-1.5 py-0.5 text-[10px] text-red-900">{e.status}</span>
                <span className="shrink-0 text-red-900">{e.sandboxID}</span>
                <span className="text-red-700">{e.message}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {bindings.length === 0 ? (
        <div className="px-4 py-10 text-center">
          <p className="text-sm font-medium text-slate-900">{t("runtime.cloud.instances.emptyTitle")}</p>
          <p className="mt-1 text-xs text-slate-500">{t("runtime.cloud.instances.emptyBody")}</p>
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[36px]">
                <input
                  type="checkbox"
                  aria-label={t("runtime.list.table.selectAll")}
                  checked={selected.size > 0 && selected.size === bindings.length}
                  ref={(el) => {
                    if (el) el.indeterminate = selected.size > 0 && selected.size < bindings.length
                  }}
                  onChange={onToggleAll}
                  data-testid="runtime-select-all"
                />
              </TableHead>
              <TableHead>{t("runtime.list.table.instance")}</TableHead>
              <TableHead>{t("runtime.list.table.agent")}</TableHead>
              <TableHead>{t("runtime.list.table.image")}</TableHead>
              <TableHead>{t("runtime.list.table.status")}</TableHead>
              <TableHead>{t("runtime.list.table.lastActive")}</TableHead>
              <TableHead>{t("runtime.list.table.createdAt")}</TableHead>
              <TableHead className="w-[100px]">{t("runtime.list.table.actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {bindings.map((b) => {
              const isTesting = testingId === b.binding_id
              const canTest = isAdmin && b.status_kind !== "terminal" && Boolean(b.project_agent_id)
              const showResult = testResult?.bindingId === b.binding_id
              return (
                <React.Fragment key={b.binding_id}>
                  <TableRow
                    onClick={() => onOpenDetail(b.sandbox_id)}
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        const target = e.target as HTMLElement
                        if (target.tagName === "INPUT" || target.tagName === "BUTTON") return
                        e.preventDefault()
                        onOpenDetail(b.sandbox_id)
                      }
                    }}
                    role="link"
                    aria-label={t("runtime.list.table.rowLabel", { agent: b.project_agent_id ?? b.sandbox_id })}
                    className="cursor-pointer hover:bg-slate-50/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-300"
                    data-testid={`runtime-row-${b.binding_id}`}
                  >
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        aria-label={t("runtime.list.table.selectOne", { id: b.sandbox_id })}
                        checked={selected.has(b.binding_id)}
                        onChange={() => onToggleOne(b.binding_id)}
                        onClick={(e) => e.stopPropagation()}
                        data-testid={`runtime-select-${b.binding_id}`}
                      />
                    </TableCell>
                    <TableCell className="max-w-[180px] truncate font-mono text-[12px] text-slate-800" title={b.sandbox_id}>
                      {b.sandbox_id}
                    </TableCell>
                    <TableCell className="font-mono text-[11px] text-slate-500">{b.project_agent_id ?? "—"}</TableCell>
                    <TableCell className="text-[12px] text-slate-600">{b.template_id}</TableCell>
                    <TableCell><SandboxStatusBadge kind={b.status_kind} status={b.status} /></TableCell>
                    <TableCell>
                      <span title={b.last_active_at} className="text-[12px] text-slate-600">
                        {relativeAgo(b.last_active_at)}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span title={b.created_at} className="text-[12px] text-slate-600">
                        {relativeAgo(b.created_at)}
                      </span>
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 gap-1 px-2 text-[12px]"
                        disabled={!canTest || isTesting || (testingId !== null && testingId !== b.binding_id)}
                        onClick={() => handleTestConnection(b)}
                        data-testid={`runtime-test-conn-${b.binding_id}`}
                      >
                        <Zap className="h-3 w-3" strokeWidth={2} />
                        {isTesting
                          ? t("runtime.connectivity.testing")
                          : t("runtime.connectivity.testButton")}
                      </Button>
                    </TableCell>
                  </TableRow>
                  {showResult && (
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={8} className="p-0">
                        <div className="border-t border-slate-100 px-4 py-3">
                          <ConnectivityResultPanel
                            result={testResult.result}
                            checkLabelFor={checkLabelFor}
                            onDismiss={() => setTestResult(null)}
                          />
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </React.Fragment>
              )
            })}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

function CloudDaemonRuntimesPanel({
  runtimes,
  loading,
  error,
  onRefresh,
}: {
  runtimes: Runtime[]
  loading: boolean
  error: unknown
  onRefresh: () => void
}) {
  const { t } = useTranslation("admin")

  if (loading) {
    return (
      <section className="rounded-lg border border-slate-200 bg-white p-4">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="mt-2 h-8 w-full" />
      </section>
    )
  }

  if (error) {
    return (
      <ErrorState
        title={t("runtime.cloud.daemonRuntimes.errors.loadFailed", { defaultValue: "无法加载沙盒内 Daemon" })}
        description={error instanceof Error ? error.message : String(error)}
        onRetry={onRefresh}
      />
    )
  }

  if (runtimes.length === 0) return null

  return (
    <section className="rounded-lg border border-slate-200 bg-white">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-4 py-3">
        <div>
          <h3 className="text-[14px] font-medium text-slate-900">
            {t("runtime.cloud.daemonRuntimes.title", { defaultValue: "沙盒内 Daemon" })}
          </h3>
          <p className="mt-0.5 max-w-3xl text-[12px] leading-relaxed text-slate-500">
            {t("runtime.cloud.daemonRuntimes.description", {
              count: runtimes.length,
              defaultValue: "运行在云端沙盒里的 parsar-daemon 进程。它们属于云端沙盒，不是本地设备。",
            })}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={onRefresh}>
          {t("runtime.list.actions.refresh", { defaultValue: "刷新" })}
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.runtime", { defaultValue: "Runtime" })}</TableHead>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.projectAgent", { defaultValue: "Project Agent" })}</TableHead>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.kind", { defaultValue: "沙盒类型" })}</TableHead>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.agentEngines", { defaultValue: "Agent 引擎" })}</TableHead>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.status", { defaultValue: "状态" })}</TableHead>
            <TableHead>{t("runtime.cloud.daemonRuntimes.table.heartbeat", { defaultValue: "最后心跳" })}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runtimes.map((runtime) => (
            <TableRow key={runtime.id} data-testid={`sandbox-daemon-runtime-row-${runtime.id}`}>
              <TableCell className="font-mono text-[12px] text-slate-800">
                <div className="space-y-1">
                  <div>{runtime.name || shortID(runtime.id)}</div>
                  <div className="text-[11px] text-slate-500">{shortID(runtime.id)}</div>
                </div>
              </TableCell>
              <TableCell className="font-mono text-[11px] text-slate-500">
                {runtimeConfigText(runtime, "project_agent_id") || "—"}
              </TableCell>
              <TableCell className="text-[12px] text-slate-600">
                {runtimeConfigText(runtime, "sandbox_kind") || runtime.provider}
              </TableCell>
              <TableCell className="text-[12px] text-slate-600">
                {formatRuntimeAgentKinds(runtime)}
              </TableCell>
              <TableCell>
                <RuntimeLivenessBadge runtime={runtime} />
              </TableCell>
              <TableCell className="text-[12px] text-slate-600" title={runtime.last_heartbeat_at ?? undefined}>
                {runtime.last_heartbeat_at ? relativeAgo(runtime.last_heartbeat_at) : "—"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  )
}

function RuntimeLivenessBadge({ runtime }: { runtime: Runtime }) {
  const { t } = useTranslation("admin")
  switch (runtime.liveness) {
    case "online":
      return <Badge variant="success" dot>{t("runtime.agentDaemon.status.online", { defaultValue: "在线" })}</Badge>
    case "pending_pairing":
      return <Badge variant="warning">{t("runtime.agentDaemon.status.pending_pairing", { defaultValue: "等待配对" })}</Badge>
    case "error":
      return <Badge variant="destructive">{t("runtime.agentDaemon.status.error", { defaultValue: "错误" })}</Badge>
    default:
      return <Badge variant="neutral">{t("runtime.agentDaemon.status.offline", { defaultValue: "离线" })}</Badge>
  }
}

function runtimeConfigText(runtime: Runtime, key: string): string {
  const raw = runtime.config[key]
  return typeof raw === "string" && raw.trim() !== "" ? raw.trim() : ""
}

function formatRuntimeAgentKinds(runtime: Runtime): string {
  const labels = supportedAgentKinds(runtime)
    .filter((kind) => kind.available)
    .map((kind) => formatAgentKindLabel(kind.kind))
  return labels.length > 0 ? labels.join(" · ") : "—"
}

function formatAgentKindLabel(kind: string): string {
  switch (kind) {
    case "claude_code":
      return "Claude Code"
    case "opencode":
      return "OpenCode"
    case "codex":
      return "Codex"
    case "pi":
      return "PI Agent"
    default:
      return kind
  }
}

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id
}

function RuntimeTabs({
  tab,
  onChange,
}: {
  tab: RuntimeTab
  onChange: (next: RuntimeTab) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="mb-4 border-b border-slate-200" role="tablist" aria-label={t("runtime.page.title")}>
      <div className="flex flex-wrap items-end gap-1">
        <RuntimeTabButton
          active={tab === "sandbox"}
          onClick={() => onChange("sandbox")}
          testId="runtime-tab-sandbox"
        >
          {t("runtime.providers.sandbox.title")}
        </RuntimeTabButton>
        <RuntimeTabButton
          active={tab === "local_device"}
          onClick={() => onChange("local_device")}
          testId="runtime-tab-local-device"
        >
          {t("runtime.providers.localDevice.title", { defaultValue: "Local Device" })}
        </RuntimeTabButton>
        <RuntimeTabButton
          active={tab === "external"}
          onClick={() => onChange("external")}
          testId="runtime-tab-external"
        >
          {t("runtime.providers.external.title")}
        </RuntimeTabButton>
      </div>
    </div>
  )
}

function RuntimeTabButton({
  active,
  onClick,
  testId,
  children,
}: {
  active: boolean
  onClick: () => void
  testId: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      data-testid={testId}
      className={
        "mb-[-1px] inline-flex min-h-10 items-center gap-2 border-b-2 px-3 text-[13px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-300 " +
        (active
          ? "border-slate-900 text-slate-900"
          : "border-transparent text-slate-500 hover:text-slate-800")
      }
    >
      {children}
    </button>
  )
}

function CloudStateBadge({ state }: { state: CloudState }) {
  const { t } = useTranslation("admin")
  const variantByState: Record<CloudState, BadgeVariant> = {
    loading: "neutral",
    notConfigured: "warning",
    ready: "success",
    error: "destructive",
    unknown: "warning",
  }
  return <Badge variant={variantByState[state]} dot={state === "ready"}>{t(`runtime.cloud.state.${state}.label`)}</Badge>
}


function ExternalAgentPanel() {
  const { t } = useTranslation("admin")
  return (
    <section className="rounded-lg border border-slate-200 bg-white p-4">
      <div className="flex items-start gap-3">
        <div className="rounded-md border border-slate-200 bg-slate-50 p-2 text-slate-700">
          <PlugZap className="h-4 w-4" strokeWidth={1.9} />
        </div>
        <div>
          <h2 className="text-[15px] font-semibold text-slate-900">{t("runtime.providers.external.title")}</h2>
          <p className="mt-1 max-w-2xl text-[13px] leading-relaxed text-slate-600">
            {t("runtime.external.body")}
          </p>
        </div>
      </div>
    </section>
  )
}

function ConfirmBulkKillDialog({
  open,
  count,
  preview,
  loading,
  onConfirm,
  onCancel,
}: {
  open: boolean
  count: number
  preview: string[]
  loading: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5 pr-5">
          <div className="shrink-0 rounded-full bg-red-100 p-2 text-red-700">
            <Skull className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">
              {t("runtime.list.confirmBulkKill.title", { count })}
            </DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {t("runtime.list.confirmBulkKill.description")}
            </DialogDescription>
            {preview.length > 0 && (
              <ul className="mt-2 list-disc space-y-0.5 pl-5 text-[12px] text-slate-600">
                {preview.map((id) => <li key={id} className="font-mono">{id}</li>)}
                {count > preview.length && (
                  <li className="list-none text-slate-400">
                    {t("runtime.list.confirmBulkKill.andMore", { count: count - preview.length })}
                  </li>
                )}
              </ul>
            )}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>
            {tc("actions.cancel")}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            disabled={loading}
            data-testid="runtime-confirm-bulk-kill"
          >
            {loading
              ? t("runtime.list.actions.killingPending", { count })
              : t("runtime.list.actions.killN", { count })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function resolveCloudState({
  status,
  statusLoading,
  statusError,
}: {
  status: RuntimeStatus | undefined
  statusLoading: boolean
  statusError: boolean
}): CloudState {
  if (statusLoading && !status) return "loading"
  if (statusError || !status) return "unknown"
  if (status.profile === "managed") return status.available ? "ready" : "error"
  if (!status.has_credential) return "notConfigured"
  return status.available ? "ready" : "error"
}

function cloudStateBody(
  t: any,
  state: CloudState,
  status: RuntimeStatus | undefined,
): string {
  const count = status?.sandbox_agent_count ?? 0
  const value = status?.credential_masked ?? ""
  if (state === "ready") return t("runtime.cloud.state.ready.body", { count })
  if (state === "error") {
    if (status?.profile === "managed") return t("runtime.cloud.state.error.managedBody")
    return value
      ? t("runtime.cloud.state.error.bodyWithCredential", { value })
      : t("runtime.cloud.state.error.body")
  }
  return t(`runtime.cloud.state.${state}.body`)
}
