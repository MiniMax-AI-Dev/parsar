import { useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { Cpu, Loader2, Trash2 } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerRow } from "../../../components/ui/ledger"
import { Skeleton } from "../../../components/ui/skeleton"
import { StatusIcon } from "../../../components/ui/status-icon"
import { InlineError } from "../../../components/runtime/InlineError"
import {
  isLocalDeviceRuntime,
  supportedAgentKinds,
  useDeleteRuntime,
  useWorkspaceRuntimes,
  type Runtime,
} from "../../../lib/api-runtimes"
import { useRelativeTime } from "../../../lib/relative-time"
import { LIVENESS_STATUS, formatAgentKindLabel } from "./runtime-status"

/** status icon · name · hostname · version · agent engines · last heartbeat · actions */
const LEDGER_COLUMNS = "14px minmax(0,1fr) 200px 64px minmax(0,220px) 88px 28px"

/** Local devices paired through parsar-daemon, one 36px ledger row each. */
export function LocalDeviceRuntimesPanel({ workspaceID }: { workspaceID: string }) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const listQ = useWorkspaceRuntimes(workspaceID, "agent_daemon")
  const deleteMut = useDeleteRuntime(workspaceID)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)

  if (listQ.isLoading) {
    return (
      <div className="-mx-6">
        <div className="h-7 border-b border-line" />
        {Array.from({ length: 3 }).map((_, i) => (
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
  if (listQ.error) {
    return (
      <ErrorState
        title={t("runtime.agentDaemon.errors.loadFailed", { defaultValue: "Failed to load local devices" })}
        description={(listQ.error as Error).message}
        onRetry={() => void listQ.refetch()}
      />
    )
  }

  const runtimes = (listQ.data ?? []).filter(isLocalDeviceRuntime)
  const title = t("runtime.providers.localDevice.title", { defaultValue: "Local Device" })

  return (
    <>
      {runtimes.length === 0 ? (
        <EmptyState
          icon={Cpu}
          title={t("runtime.agentDaemon.empty", {
            defaultValue: "No local devices paired yet. Use the button above to generate a pairing command.",
          })}
        />
      ) : (
        <Ledger columns={LEDGER_COLUMNS} className="-mx-6" role="listbox" aria-label={title}>
          <LedgerHeader>
            <span />
            <span>{t("runtime.agentDaemon.table.name", { defaultValue: "Name" })}</span>
            <span>{t("runtime.agentDaemon.table.hostname", { defaultValue: "Hostname" })}</span>
            <span>{t("runtime.agentDaemon.table.version", { defaultValue: "Version" })}</span>
            <span>{t("runtime.agentDaemon.table.agentEngines", { defaultValue: "Agent engines" })}</span>
            <span className="text-right">{t("runtime.agentDaemon.table.heartbeat", { defaultValue: "Last heartbeat" })}</span>
            <span />
          </LedgerHeader>
          <ul className="m-0 list-none p-0">
            {runtimes.map((r) => (
              <DaemonRuntimeRow
                key={r.id}
                runtime={r}
                age={fmtAgo(r.last_heartbeat_at)}
                onDelete={() => setDeleteTarget({ id: r.id, name: r.name })}
              />
            ))}
          </ul>
        </Ledger>
      )}

      {deleteTarget && (
        <ConfirmDeleteRuntimeDialog
          targetName={deleteTarget.name}
          pending={deleteMut.isPending}
          error={deleteMut.error as Error | undefined}
          onCancel={() => { setDeleteTarget(null); deleteMut.reset() }}
          onConfirm={() => {
            deleteMut.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) })
          }}
        />
      )}
    </>
  )
}

function DaemonRuntimeRow({
  runtime,
  age,
  onDelete,
}: {
  runtime: Runtime
  age: string
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const activeRequests = daemonActiveRequests(runtime)
  const statusLabel = t(`runtime.agentDaemon.status.${runtime.liveness}`, { defaultValue: runtime.liveness })
  const engines = formatAgentKinds(runtime, t)

  return (
    <LedgerRow data-testid={`agent-daemon-row-${runtime.id}`} title={runtime.id}>
      <StatusIcon status={LIVENESS_STATUS[runtime.liveness]} title={statusLabel} />
      <span className="flex min-w-0 items-baseline gap-1.5">
        <span className="truncate font-medium">{runtime.name}</span>
        {runtime.liveness !== "online" && (
          <span className="shrink-0 text-xs text-fg-muted">· {statusLabel}</span>
        )}
        {runtime.liveness === "online" && activeRequests !== null && activeRequests > 0 && (
          <span className="shrink-0 text-xs tabular-nums text-fg-muted">
            · {t("runtime.agentDaemon.load.active", { count: activeRequests, defaultValue: "{{count}} running" })}
          </span>
        )}
      </span>
      <span className={runtime.hostname ? "truncate font-mono text-xs text-fg" : "font-mono text-xs text-fg-muted"} title={runtime.hostname || undefined}>
        {runtime.hostname || "—"}
      </span>
      <span className={runtime.version ? "truncate font-mono text-xs tabular-nums text-fg" : "font-mono text-xs text-fg-muted"}>
        {runtime.version || "—"}
      </span>
      <span className="truncate text-xs text-fg-muted" title={engines || undefined}>{engines || "—"}</span>
      <span className="truncate text-right text-xs text-fg-muted" title={runtime.last_heartbeat_at ?? undefined}>{age}</span>
      <RowActions>
        <ActionIconButton
          icon={Trash2}
          tone="danger"
          label={t("runtime.agentDaemon.actions.delete", { defaultValue: "Delete device" })}
          onClick={onDelete}
          data-testid={`agent-daemon-delete-${runtime.id}`}
        />
      </RowActions>
    </LedgerRow>
  )
}

/** "Claude Code 1.0.112 · Codex 0.42.0"; kinds that are not detected are left out. */
function formatAgentKinds(runtime: Runtime, t: TFunction<"admin">): string {
  return supportedAgentKinds(runtime)
    .filter((kind) => kind.available)
    .map((kind) => {
      const label = formatAgentKindLabel(kind.kind)
      return kind.version
        ? t("runtime.agentDaemon.agentKind.version", { label, version: kind.version, defaultValue: "{{label}} {{version}}" })
        : label
    })
    .join(" · ")
}

function daemonActiveRequests(runtime: Runtime): number | null {
  const raw = runtime.config.agent_daemon_active_requests
  if (typeof raw === "number" && Number.isFinite(raw)) return Math.max(0, Math.trunc(raw))
  if (typeof raw === "string" && raw.trim() !== "") {
    const parsed = Number(raw)
    if (Number.isFinite(parsed)) return Math.max(0, Math.trunc(parsed))
  }
  return null
}

function ConfirmDeleteRuntimeDialog({
  targetName,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  targetName: string
  pending: boolean
  error?: Error
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  return (
    <AlertDialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("runtime.agentDaemon.delete.title", { name: targetName, defaultValue: "Delete device {{name}}" })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("runtime.agentDaemon.delete.description", {
              defaultValue: "Once deleted, this device can no longer accept new tasks; running tasks are unaffected. This action cannot be undone.",
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && <InlineError>{error.message}</InlineError>}
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline" disabled={pending}>
              {tc("actions.cancel")}
            </Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant="destructive"
              onClick={(e) => { e.preventDefault(); onConfirm() }}
              disabled={pending}
            >
              {pending && <Loader2 className="animate-spin" />}
              {t("runtime.agentDaemon.delete.confirm", { defaultValue: "Delete" })}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
