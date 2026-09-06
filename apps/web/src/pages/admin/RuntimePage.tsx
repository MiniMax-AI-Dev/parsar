import React, { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Loader2, Skull, Zap } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ConnectivityResultPanel } from "../../components/runtime/ConnectivityResultPanel"
import { RuntimeCredentialCard } from "../../components/runtime/RuntimeCredentialCard"
import { RuntimeStatusBanner } from "../../components/runtime/RuntimeStatusBanner"
import { PairDaemonDialog } from "../../components/admin/PairDaemonDialog"
import { LocalDeviceRuntimesPanel } from "./runtimes/LocalDeviceRuntimesPanel"
import { LIVENESS_STATUS, formatAgentKindLabel } from "./runtimes/runtime-status"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../components/ui/alert-dialog"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerId, LedgerRow, col } from "../../components/ui/ledger"
import { Select } from "../../components/ui/select"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import { useRuntimeStatus, type ConnectivityResult, type RuntimeStatus } from "../../lib/api-runtime"
import {
  isSandboxDaemonRuntime,
  supportedAgentKinds,
  useWorkspaceRuntimes,
  type Runtime,
} from "../../lib/api-runtimes"
import { isSandboxPairingExpired } from "../../lib/sandbox-runtime"
import {
  killSandboxRequestRaw,
  useSandboxConnectivityTest,
  useWorkspaceSandboxes,
  type SandboxBinding,
  type SandboxStatusKind,
} from "../../lib/api-sandbox"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useRelativeTime } from "../../lib/relative-time"
import { useNow } from "../../lib/use-now"
import { useWorkspaceId } from "../../lib/workspace"

type RuntimeTab = "sandbox" | "local_device" | "external"
type CloudState = "loading" | "notConfigured" | "ready" | "error" | "unknown"
type SortKey = "last_active" | "created_at" | "agent"

const TABS: RuntimeTab[] = ["local_device", "sandbox", "external"]

const SANDBOX_STATUS: Record<SandboxStatusKind, StatusKind> = {
  live: "completed",
  transient: "running",
  terminal: "cancelled",
}

/** select · sandbox id · agent · status · image · last active · created · actions */
const INSTANCE_COLUMNS = [col.check(), col.id(200, 2), col.id(120), col.meta(112), col.meta(120), col.age(80), col.age(80), col.actions(1)]
/** status icon · runtime · agent · sandbox kind · agent engines · last heartbeat */
const DAEMON_COLUMNS = [col.icon(), col.title(), col.id(140), col.meta(104), col.meta(120), col.age(96)]

function sortBindings(bindings: SandboxBinding[], sortKey: SortKey): SandboxBinding[] {
  const copy = bindings.slice()
  if (sortKey === "agent") {
    copy.sort((a, b) => (a.agent_id ?? "").localeCompare(b.agent_id ?? ""))
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
  const workspaceID = useWorkspaceId()
  const statusQuery = useRuntimeStatus(workspaceID)
  const sandboxesQuery = useWorkspaceSandboxes(workspaceID)
  const daemonRuntimesQuery = useWorkspaceRuntimes(workspaceID ?? "", "agent_daemon")
  const workspacesQ = useMyWorkspaces()

  const [tab, setTab] = useState<RuntimeTab>("local_device")
  const [pairOpen, setPairOpen] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sortKey, setSortKey] = useState<SortKey>("last_active")
  const [confirming, setConfirming] = useState(false)
  const [bulkPending, setBulkPending] = useState(false)
  const [bulkErrors, setBulkErrors] = useState<
    { sandboxID: string; status: number | string; message: string }[]
  >([])

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
  // Offline rows are stale: runtime row is owned by the agent
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
    const toKill = activeBindings.filter((b) => selected.has(b.binding_id) && b.agent_id)
    const errors: { sandboxID: string; status: number | string; message: string }[] = []
    for (const b of toKill) {
      try {
        await killSandboxRequestRaw(workspaceID, b.agent_id as string)
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

  const tabLabel: Record<RuntimeTab, string> = {
    local_device: t("runtime.providers.localDevice.title", { defaultValue: "Local Device" }),
    sandbox: t("runtime.providers.sandbox.title"),
    external: t("runtime.providers.external.title"),
  }

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <Tabs value={tab} onValueChange={(v) => setTab(v as RuntimeTab)} className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={t("runtime.page.title")}
          subtitleFor="runtime.page.title"
          action={
            <>
              <SettingsTabs active="runtime" />
              {tab === "local_device" && workspaceID && (
                <Button onClick={() => setPairOpen(true)} data-testid="agent-daemon-pair-button">
                  {t("runtime.agentDaemon.actions.pair", { defaultValue: "Pair a new device" })}
                </Button>
              )}
            </>
          }
        />
        <div className="flex h-10 shrink-0 items-center border-b border-line px-4">
          <TabsList aria-label={t("runtime.page.title")}>
            {TABS.map((id) => (
              <TabsTrigger key={id} value={id} data-testid={`runtime-tab-${id.replace("_", "-")}`}>
                {tabLabel[id]}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-10">

        <TabsContent value="local_device" className="mt-0">
          {workspaceID && <LocalDeviceRuntimesPanel workspaceID={workspaceID} />}
        </TabsContent>

        <TabsContent value="sandbox" className="mt-0">
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
              onClearBulkErrors={() => setBulkErrors([])}
            onConfirmBulkKill={() => setConfirming(true)}
          />
        </TabsContent>

        <TabsContent value="external" className="mt-0 pt-4">
          <p className="max-w-2xl text-sm text-fg">{t("runtime.external.body")}</p>
        </TabsContent>
        </div>
      </Tabs>

      {workspaceID && (
        <PairDaemonDialog open={pairOpen} onClose={() => setPairOpen(false)} workspaceID={workspaceID} />
      )}

      <ConfirmBulkKillDialog
        open={confirming}
        count={selected.size}
        preview={activeBindings
          .filter((b) => selected.has(b.binding_id))
          .slice(0, 5)
          .map((b) => b.sandbox_id)}
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
  onClearBulkErrors: () => void
  onConfirmBulkKill: () => void
}) {
  const showCredentialControl =
    cloudState !== "loading" && cloudState !== "unknown" && status?.profile !== "managed"
  const showInstances = !statusError && Boolean(workspaceID)

  return (
    <div>
      <RuntimeStatusBanner workspaceID={workspaceID} />

      {showCredentialControl && (
        <RuntimeCredentialCard workspaceID={workspaceID} isAdmin={isAdmin} className="mt-4" />
      )}

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
          onClearBulkErrors={onClearBulkErrors}
          onConfirmBulkKill={onConfirmBulkKill}
        />
      ) : null}
    </div>
  )
}

/** 12px/500 section head with an optional control cluster on the right. */
function SectionHead({ title, meta, children }: { title: string; meta?: number; children?: React.ReactNode }) {
  return (
    <div className="mt-6 flex min-h-7 items-center justify-between gap-3">
      <h2 className="flex items-baseline gap-1.5 text-xs font-medium text-fg">
        <span>{title}</span>
        {meta !== undefined && <span className="font-normal tabular-nums text-fg-muted">{meta}</span>}
      </h2>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}

function LedgerSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="-mx-6">
      <div className="h-7 border-b border-line" />
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line px-4">
          <Skeleton className="h-3.5 w-3.5 rounded-full" />
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-16" />
        </div>
      ))}
    </div>
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
  onClearBulkErrors: () => void
  onConfirmBulkKill: () => void
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const checkLabelFor = useConnectivityCheckLabel()
  const [testingId, setTestingId] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<{ bindingId: string; result: ConnectivityResult } | null>(null)
  const connTest = useSandboxConnectivityTest()

  function handleTestConnection(b: SandboxBinding) {
    if (!workspaceID || !b.agent_id) return
    setTestingId(b.binding_id)
    setTestResult(null)
    connTest.mutateAsync({ workspaceID, agentID: b.agent_id }).then(
      (result) => {
        setTestResult({ bindingId: b.binding_id, result })
        setTestingId(null)
      },
      () => setTestingId(null),
    )
  }

  const title = t("runtime.cloud.instances.title")

  return (
    <section>
      <SectionHead title={title} meta={loading || error ? undefined : bindings.length}>
        {!loading && !error && bindings.length > 0 && (
          <>
            <Select
              value={sortKey}
              onChange={(e) => onSortChange(e.target.value as SortKey)}
              aria-label={t("runtime.list.sort.label")}
              wrapperClassName="w-[180px]"
              className="h-6 text-xs"
              data-testid="runtime-sort"
            >
              <option value="last_active">{t("runtime.list.sort.lastActive")}</option>
              <option value="created_at">{t("runtime.list.sort.createdAt")}</option>
              <option value="agent">{t("runtime.list.sort.agent")}</option>
            </Select>
            <Button
              size="sm"
              variant="outline"
              disabled={selected.size === 0 || bulkPending}
              onClick={onConfirmBulkKill}
              data-testid="runtime-bulk-kill"
            >
              <Skull strokeWidth={1.5} aria-hidden="true" />
              {t("runtime.list.actions.bulkKill", { count: selected.size })}
            </Button>
          </>
        )}
      </SectionHead>

      {bulkErrors.length > 0 && (
        <div className="mt-2 text-sm" role="alert">
          <div className="flex h-7 items-center gap-1.5">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate font-medium text-fg">
              {t("runtime.list.errors.bulkKillPartial")}
              <span className="font-normal tabular-nums text-fg-muted"> {bulkErrors.length}</span>
            </span>
            <Button variant="ghost" size="sm" onClick={onClearBulkErrors} data-testid="runtime-bulk-error-dismiss">
              {t("runtime.list.errors.bulkKillDismiss")}
            </Button>
          </div>
          <ul className="m-0 max-h-40 list-none overflow-y-auto p-0">
            {bulkErrors.map((e) => (
              <li key={e.sandboxID} className="flex h-8 items-center gap-2 border-t border-line font-mono text-xs">
                <span className="w-8 shrink-0 tabular-nums text-fg-muted">{e.status}</span>
                <span className="shrink-0 text-fg">{e.sandboxID}</span>
                <span className="min-w-0 truncate text-fg-muted">{e.message}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {loading ? (
        <LedgerSkeleton />
      ) : error ? (
        <ErrorState
          title={t("runtime.list.errors.loadFailed")}
          description={error instanceof Error ? error.message : String(error)}
          onRetry={onRefresh}
        />
      ) : bindings.length === 0 ? (
        <EmptyState
          title={t("runtime.cloud.instances.emptyTitle")}
          description={t("runtime.cloud.instances.emptyBody")}
          className="py-10"
        />
      ) : (
        <Ledger columns={INSTANCE_COLUMNS} className="-mx-6" role="listbox" aria-label={title} aria-multiselectable>
          <LedgerHeader>
            <span className="flex items-center">
              <input
                type="checkbox"
                className="h-3.5 w-3.5 accent-accent"
                aria-label={t("runtime.list.table.selectAll")}
                checked={selected.size > 0 && selected.size === bindings.length}
                ref={(el) => {
                  if (el) el.indeterminate = selected.size > 0 && selected.size < bindings.length
                }}
                onChange={onToggleAll}
                data-testid="runtime-select-all"
              />
            </span>
            <span>{t("runtime.list.table.instance")}</span>
            <span>{t("runtime.list.table.agent")}</span>
            <span>{t("runtime.list.table.status")}</span>
            <span>{t("runtime.list.table.image")}</span>
            <span className="text-right">{t("runtime.list.table.lastActive")}</span>
            <span className="text-right">{t("runtime.list.table.createdAt")}</span>
            <span />
          </LedgerHeader>
          <ul className="m-0 list-none p-0">
            {bindings.map((b) => {
              const isTesting = testingId === b.binding_id
              const canTest = isAdmin && b.status_kind !== "terminal" && Boolean(b.agent_id)
              const showResult = testResult?.bindingId === b.binding_id
              const rowLabel = t("runtime.list.table.rowLabel", { agent: b.agent_id ?? b.sandbox_id })
              return (
                <React.Fragment key={b.binding_id}>
                  <LedgerRow
                    selected={selected.has(b.binding_id)}
                    aria-label={rowLabel}
                    data-testid={`runtime-row-${b.binding_id}`}
                  >
                    <span className="flex items-center" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        className="h-3.5 w-3.5 accent-accent"
                        aria-label={t("runtime.list.table.selectOne", { id: b.sandbox_id })}
                        checked={selected.has(b.binding_id)}
                        onChange={() => onToggleOne(b.binding_id)}
                        data-testid={`runtime-select-${b.binding_id}`}
                      />
                    </span>
                    <span className="truncate font-mono text-xs text-fg" title={b.sandbox_id}>{b.sandbox_id}</span>
                    <LedgerId>{b.agent_id ?? "—"}</LedgerId>
                    <span className="flex min-w-0 items-center gap-1.5">
                      <StatusIcon status={SANDBOX_STATUS[b.status_kind]} />
                      <span className="truncate">{b.status}</span>
                    </span>
                    <span className="truncate text-xs text-fg-muted" title={b.template_id}>{b.template_id}</span>
                    <span className="truncate text-right text-xs text-fg-muted" title={b.last_active_at}>{fmtAgo(b.last_active_at)}</span>
                    <span className="truncate text-right text-xs text-fg-muted" title={b.created_at}>{fmtAgo(b.created_at)}</span>
                    <RowActions>
                      <ActionIconButton
                        icon={Zap}
                        label={isTesting ? t("runtime.connectivity.testing") : t("runtime.connectivity.testButton")}
                        busy={isTesting}
                        disabled={!canTest || (testingId !== null && testingId !== b.binding_id)}
                        onClick={() => handleTestConnection(b)}
                        data-testid={`runtime-test-conn-${b.binding_id}`}
                      />
                    </RowActions>
                  </LedgerRow>
                  {showResult && (
                    <li className="border-b border-line pl-[50px] pr-6">
                      <ConnectivityResultPanel
                        result={testResult.result}
                        checkLabelFor={checkLabelFor}
                        onDismiss={() => setTestResult(null)}
                      />
                    </li>
                  )}
                </React.Fragment>
              )
            })}
          </ul>
        </Ledger>
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
  const fmtAgo = useRelativeTime()
  const title = t("runtime.cloud.daemonRuntimes.title", { defaultValue: "Sandbox daemons" })

  if (loading) {
    return (
      <section>
        <SectionHead title={title} />
        <LedgerSkeleton rows={2} />
      </section>
    )
  }

  if (error) {
    return (
      <section>
        <SectionHead title={title} />
        <ErrorState
          title={t("runtime.cloud.daemonRuntimes.errors.loadFailed", { defaultValue: "Failed to load sandbox daemons" })}
          description={error instanceof Error ? error.message : String(error)}
          onRetry={onRefresh}
        />
      </section>
    )
  }

  if (runtimes.length === 0) return null

  return (
    <section>
      <SectionHead title={title} meta={runtimes.length} />
      <Ledger columns={DAEMON_COLUMNS} className="-mx-6" role="listbox" aria-label={title}>
        <LedgerHeader>
          <span />
          <span>{t("runtime.cloud.daemonRuntimes.table.runtime", { defaultValue: "Runtime" })}</span>
          <span>{t("runtime.cloud.daemonRuntimes.table.agent", { defaultValue: "Agent" })}</span>
          <span>{t("runtime.cloud.daemonRuntimes.table.kind", { defaultValue: "Sandbox type" })}</span>
          <span>{t("runtime.cloud.daemonRuntimes.table.agentEngines", { defaultValue: "Agent engines" })}</span>
          <span className="text-right">{t("runtime.cloud.daemonRuntimes.table.heartbeat", { defaultValue: "Last heartbeat" })}</span>
        </LedgerHeader>
        <ul className="m-0 list-none p-0">
          {runtimes.map((runtime) => {
            const state = sandboxDaemonState(runtime)
            return (
              <LedgerRow key={runtime.id} data-testid={`sandbox-daemon-runtime-row-${runtime.id}`} title={runtime.id}>
                <StatusIcon status={state.status} title={t(state.labelKey, { defaultValue: state.fallback })} />
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate font-medium">{runtime.name || shortID(runtime.id)}</span>
                  {state.status !== "completed" && (
                    <span className="shrink-0 text-xs text-fg-muted">· {t(state.labelKey, { defaultValue: state.fallback })}</span>
                  )}
                </span>
                <LedgerId>{runtimeConfigText(runtime, "agent_id") || "—"}</LedgerId>
                <span className="truncate text-xs text-fg-muted">{runtimeConfigText(runtime, "sandbox_kind") || runtime.provider}</span>
                <span className="truncate text-xs text-fg-muted">{formatRuntimeAgentKinds(runtime)}</span>
                <span className="truncate text-right text-xs text-fg-muted" title={runtime.last_heartbeat_at ?? undefined}>
                  {fmtAgo(runtime.last_heartbeat_at)}
                </span>
              </LedgerRow>
            )
          })}
        </ul>
      </Ledger>
    </section>
  )
}

type DaemonStateKey =
  | "runtime.cloud.daemonRuntimes.status.timedOut"
  | "runtime.cloud.daemonRuntimes.status.preparing"
  | "runtime.agentDaemon.status.online"
  | "runtime.agentDaemon.status.offline"
  | "runtime.agentDaemon.status.error"
  | "runtime.agentDaemon.status.pending_pairing"

function sandboxDaemonState(runtime: Runtime): { status: StatusKind; labelKey: DaemonStateKey; fallback: string } {
  if (runtime.liveness === "pending_pairing") {
    return isSandboxPairingExpired(runtime)
      ? { status: "failed", labelKey: "runtime.cloud.daemonRuntimes.status.timedOut", fallback: "Startup timed out" }
      : { status: "running", labelKey: "runtime.cloud.daemonRuntimes.status.preparing", fallback: "Preparing" }
  }
  return {
    status: LIVENESS_STATUS[runtime.liveness],
    labelKey: `runtime.agentDaemon.status.${runtime.liveness}` as DaemonStateKey,
    fallback: runtime.liveness,
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

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id
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
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !loading) onCancel()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("runtime.list.confirmBulkKill.title", { count })}</AlertDialogTitle>
          <AlertDialogDescription>{t("runtime.list.confirmBulkKill.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        {preview.length > 0 && (
          <ul className="m-0 list-none p-0">
            {preview.map((id) => (
              <li key={id} className="flex h-7 items-center border-b border-line font-mono text-xs text-fg last:border-b-0">
                {id}
              </li>
            ))}
            {count > preview.length && (
              <li className="flex h-7 items-center text-xs text-fg-muted">
                {t("runtime.list.confirmBulkKill.andMore", { count: count - preview.length })}
              </li>
            )}
          </ul>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline" disabled={loading}>{tc("actions.cancel")}</Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant="destructive"
              onClick={(e) => { e.preventDefault(); onConfirm() }}
              disabled={loading}
              data-testid="runtime-confirm-bulk-kill"
            >
              {loading && <Loader2 className="animate-spin" />}
              {loading
                ? t("runtime.list.actions.killingPending", { count })
                : t("runtime.list.actions.killN", { count })}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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

