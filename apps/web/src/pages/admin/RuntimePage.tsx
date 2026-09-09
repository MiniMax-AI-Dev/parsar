import React, { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, Loader2, Skull, Zap } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ConnectivityResultPanel } from "../../components/runtime/ConnectivityResultPanel"
import { PairDaemonDialog } from "../../components/admin/PairDaemonDialog"
import { RuntimeLedger } from "./runtimes/RuntimeLedger"
import { RuntimeCloudPanel, RuntimeInstancesError } from "./runtimes/RuntimeCloudPanel"
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
import type { StatusKind } from "../../components/ui/status-icon"
import { Ledger, LedgerHeader, LedgerId, LedgerRow, SelectableStatus, col } from "../../components/ui/ledger"
import { Select } from "../../components/ui/select"
import { Skeleton } from "../../components/ui/skeleton"
import { ApiError } from "../../lib/api-client"
import { useRuntimeStatus, type ConnectivityResult } from "../../lib/api-runtime"
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
import { SectionHead } from "../../components/ui/section"

type SortKey = "last_active" | "created_at" | "agent"


const SANDBOX_STATUS: Record<SandboxStatusKind, StatusKind> = {
  live: "completed",
  transient: "running",
  terminal: "cancelled",
}

/** select · sandbox id · agent · status · image · last active · created · actions */
const INSTANCE_COLUMNS = [col.icon(), col.id(200, 2), col.id(120), col.meta(112), col.meta(120), col.age(80), col.age(80), col.actions(1)]

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
  const workspacesQ = useMyWorkspaces()

  type RuntimeTab = "environments" | "instances"
  const [tab, setTab] = useState<RuntimeTab>("environments")
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

  const bindings = useMemo(
    () => sortBindings(sandboxesQuery.data ?? [], sortKey),
    [sandboxesQuery.data, sortKey],
  )
  const activeBindings = sandboxesQuery.error ? [] : bindings
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

  return (
    <AdminLayout activeMenu="runtime" fullBleed>
      {/* Two objects, two shapes: a runtime is a registered place an agent can
          run, an instance is a live sandbox that will be reclaimed. Tabs are
          for different shapes; the placements within a runtime are groups. */}
      <Tabs value={tab} onValueChange={(v) => setTab(v as RuntimeTab)} className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={t("runtime.page.title")}
          subtitleFor="runtime.page.title"
          action={
            workspaceID && tab === "environments" ? (
              <Button onClick={() => setPairOpen(true)} data-testid="agent-daemon-pair-button">
                {t("runtime.agentDaemon.actions.pair", { defaultValue: "Pair a new device" })}
              </Button>
            ) : undefined
          }
        />
        <div className="flex h-10 shrink-0 items-center border-b border-line px-4">
          <TabsList aria-label={t("runtime.page.title")}>
            <TabsTrigger value="environments">{t("runtime.tabs.environments")}</TabsTrigger>
            <TabsTrigger value="instances">{t("runtime.tabs.instances")}</TabsTrigger>
          </TabsList>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-10">
        <TabsContent value="environments" className="mt-0">
          {workspaceID && <RuntimeLedger workspaceID={workspaceID} />}
        </TabsContent>

        <TabsContent value="instances" className="mt-0">
          <RuntimeCloudPanel
            workspaceID={workspaceID}
            status={statusQuery.data}
            statusLoading={statusQuery.isLoading}
            statusError={Boolean(statusQuery.error)}
            isAdmin={isAdmin}
          >
            <CloudInstancesPanel
              workspaceID={workspaceID}
              isAdmin={isAdmin}
              bindings={activeBindings}
              loading={sandboxesQuery.isLoading}
              error={sandboxesQuery.error}
              sortKey={sortKey}
              selected={selected}
              bulkPending={bulkPending}
              bulkErrors={bulkErrors}
              onRefresh={() => {
                void statusQuery.refetch()
                void sandboxesQuery.refetch()
              }}
              onSortChange={setSortKey}
              onToggleOne={toggleOne}
              onToggleAll={toggleAll}
              onClearBulkErrors={() => setBulkErrors([])}
              onConfirmBulkKill={() => setConfirming(true)}
            />
          </RuntimeCloudPanel>
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
      <SectionHead
        title={title}
        meta={loading || error ? undefined : bindings.length}
        className="mt-6"
        action={!loading && !error && bindings.length > 0 && (
          <div className="flex items-center gap-2">
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
          </div>
        )}
      />

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
        <RuntimeInstancesError error={error} onRetry={onRefresh} />
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
              // Names the row. It used to say "open runtime detail", which a
              // screen reader announced as an action on a row that has no
              // click handler and no detail view behind it.
              const rowLabel = t("runtime.list.table.rowLabel", { id: b.sandbox_id })
              return (
                <React.Fragment key={b.binding_id}>
                  <LedgerRow
                    selected={selected.has(b.binding_id)}
                    aria-label={rowLabel}
                    data-testid={`runtime-row-${b.binding_id}`}
                  >
                    <SelectableStatus
                      status={SANDBOX_STATUS[b.status_kind]}
                      selected={selected.has(b.binding_id)}
                      selecting={selected.size > 0}
                      onSelectedChange={() => onToggleOne(b.binding_id)}
                      label={t("runtime.list.table.selectOne", { id: b.sandbox_id })}
                    />
                    <span className="truncate font-mono text-xs text-fg" title={b.sandbox_id}>{b.sandbox_id}</span>
                    <LedgerId>{b.agent_id ?? "—"}</LedgerId>
                    {/* The state in the reader's language. This column used to
                        print `b.status` — the server's own granular string
                        (`killed_by_user`) — untranslated, next to fully
                        translated neighbours, and the glyph repeated it as a
                        tooltip. The exact server word is on the row now. */}
                    <span className="truncate" title={b.status}>{t(`runtime.list.table.state.${b.status_kind}`)}</span>
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
