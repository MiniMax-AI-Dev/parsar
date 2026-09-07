import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Cpu, Trash2 } from "lucide-react"

import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Ledger, LedgerGroup, LedgerHeader, LedgerId, LedgerRow, col } from "../../../components/ui/ledger"
import { Skeleton } from "../../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../../components/ui/status-icon"
import {
  runtimePlacementOf,
  supportedAgentKinds,
  useDeleteRuntime,
  useWorkspaceRuntimes,
  type Runtime,
  type RuntimePlacement,
} from "../../../lib/api-runtimes"
import { isSandboxPairingExpired, sandboxRuntimeAgentID } from "../../../lib/sandbox-runtime"
import { useRelativeTime } from "../../../lib/relative-time"
import { ConfirmDeleteRuntimeDialog } from "./DeleteRuntimeDialog"
import { LIVENESS_STATUS, formatAgentKindLabel } from "./runtime-status"

/**
 * Every runtime the workspace has, in one list, grouped by where it sits.
 * The three placements used to be three tabs, but a tab is for a different
 * shape of content and every row here is the same shape — a runtime.
 */
/** status · name (+load) · host / agent · version · agent engines · last heartbeat · actions */
const LEDGER_COLUMNS = [col.icon(), col.title(), col.id(150), col.id(72, 0.4), col.meta(132), col.age(96), col.actions(1)]

const PLACEMENTS: RuntimePlacement[] = ["local_device", "cloud_sandbox", "external_agent"]

export function RuntimeLedger({ workspaceID }: { workspaceID: string }) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const listQ = useWorkspaceRuntimes(workspaceID)
  const deleteMut = useDeleteRuntime(workspaceID)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)

  const grouped = useMemo(() => {
    const out = new Map<RuntimePlacement, Runtime[]>(PLACEMENTS.map((p) => [p, []]))
    for (const runtime of listQ.data ?? []) {
      const placement = runtimePlacementOf(runtime)
      // A sandbox runtime row is owned by its agent under a deterministic
      // name, not by the sandbox's life: when the sandbox dies the row stays
      // and the heartbeat sweeper only flips it offline. Showing those would
      // read as live daemons that are not there.
      if (placement === "cloud_sandbox" && runtime.liveness === "offline") continue
      const bucket = out.get(placement)
      if (bucket) bucket.push(runtime)
    }
    return out
  }, [listQ.data])

  const total = useMemo(
    () => PLACEMENTS.reduce((n, p) => n + (grouped.get(p)?.length ?? 0), 0),
    [grouped],
  )

  if (listQ.isLoading) {
    return (
      <div className="-mx-6">
        <div className="h-7 border-b border-line" />
        {Array.from({ length: 4 }).map((_, i) => (
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
        title={t("runtime.ledger.loadError")}
        description={(listQ.error as Error).message}
        onRetry={() => void listQ.refetch()}
      />
    )
  }
  if (total === 0) {
    return (
      <EmptyState
        icon={Cpu}
        title={t("runtime.ledger.emptyTitle")}
        description={t("runtime.ledger.emptyDescription")}
      />
    )
  }

  return (
    <>
      <Ledger columns={LEDGER_COLUMNS} className="-mx-6" role="listbox" aria-label={t("runtime.ledger.label")}>
        <LedgerHeader>
          <span />
          <span>{t("runtime.agentDaemon.table.name", { defaultValue: "Name" })}</span>
          <span>{t("runtime.ledger.host")}</span>
          <span>{t("runtime.agentDaemon.table.version", { defaultValue: "Version" })}</span>
          <span>{t("runtime.agentDaemon.table.agentEngines", { defaultValue: "Agent engines" })}</span>
          <span className="text-right">{t("runtime.agentDaemon.table.heartbeat", { defaultValue: "Last heartbeat" })}</span>
          <span />
        </LedgerHeader>
        {PLACEMENTS.map((placement) => {
          const rows = grouped.get(placement) ?? []
          if (rows.length === 0) return null
          return (
            <LedgerGroup
              key={placement}
              label={t(`runtime.placement.${placement}` as never)}
              count={rows.length}
            >
              {rows.map((runtime) => (
                <RuntimeRow
                  key={runtime.id}
                  runtime={runtime}
                  placement={placement}
                  age={fmtAgo(runtime.last_heartbeat_at)}
                  onDelete={() => setDeleteTarget({ id: runtime.id, name: runtime.name })}
                />
              ))}
            </LedgerGroup>
          )
        })}
      </Ledger>

      {deleteTarget && (
        <ConfirmDeleteRuntimeDialog
          targetName={deleteTarget.name}
          pending={deleteMut.isPending}
          error={deleteMut.error as Error | undefined}
          onCancel={() => {
            setDeleteTarget(null)
            deleteMut.reset()
          }}
          onConfirm={() => {
            deleteMut.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) })
          }}
        />
      )}
    </>
  )
}

function RuntimeRow({ runtime, placement, age, onDelete }: {
  runtime: Runtime
  placement: RuntimePlacement
  age: string
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const state = runtimeState(runtime, placement, t)
  const engines = formatAgentKinds(runtime)
  const activeRequests = daemonActiveRequests(runtime)
  // A local device is identified by the machine it runs on; a sandbox row by
  // the agent that owns it, which is the only thing telling two apart.
  const secondary = placement === "cloud_sandbox"
    ? sandboxRuntimeAgentID(runtime)
    : runtime.hostname
  // Deleting a paired device is a real admin action. A sandbox row is not the
  // sandbox, so removing it would only orphan the agent's binding.
  const deletable = placement === "local_device"

  return (
    <LedgerRow data-testid={`runtime-ledger-row-${runtime.id}`} title={runtime.id}>
      <StatusIcon status={state.status} title={state.label} />
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-medium">{runtime.name}</span>
        {runtime.liveness !== "online" && (
          <span className="shrink-0 text-xs text-fg-muted">· {state.label}</span>
        )}
        {runtime.liveness === "online" && activeRequests !== null && activeRequests > 0 && (
          <span className="shrink-0 text-xs tabular-nums text-fg-muted">
            · {t("runtime.agentDaemon.load.active", { count: activeRequests, defaultValue: "{{count}} running" })}
          </span>
        )}
      </span>
      <LedgerId>{secondary || "—"}</LedgerId>
      <span className={runtime.version ? "truncate font-mono text-xs tabular-nums text-fg" : "font-mono text-xs text-fg-muted"}>
        {runtime.version || "—"}
      </span>
      <span className="truncate text-xs text-fg-muted" title={engines || undefined}>{engines || "—"}</span>
      <span className="truncate text-right text-xs text-fg-muted" title={runtime.last_heartbeat_at ?? undefined}>
        {age}
      </span>
      {deletable ? (
        <RowActions>
          <ActionIconButton
            icon={Trash2}
            tone="danger"
            label={t("runtime.agentDaemon.actions.delete", { defaultValue: "Delete device" })}
            onClick={onDelete}
            data-testid={`runtime-ledger-delete-${runtime.id}`}
          />
        </RowActions>
      ) : (
        <span />
      )}
    </LedgerRow>
  )
}

/**
 * A sandbox starts itself, so "pending pairing" reads as 准备中 while the token
 * is good and 启动超时 once it is not — nothing will pair it after that, and
 * the heartbeat sweeper only demotes rows that were once online, so it would
 * otherwise sit at "pending" forever. A local device is the opposite: a human
 * is going to run the command, and 等待配对 is exactly right until they do.
 */
function runtimeState(
  runtime: Runtime,
  placement: RuntimePlacement,
  t: ReturnType<typeof useTranslation<"admin">>["t"],
): { status: StatusKind; label: string } {
  if (placement === "cloud_sandbox" && runtime.liveness === "pending_pairing") {
    return isSandboxPairingExpired(runtime)
      ? { status: "failed", label: t("runtime.ledger.pairingExpired") }
      : { status: "running", label: t("runtime.ledger.preparing") }
  }
  return {
    status: LIVENESS_STATUS[runtime.liveness],
    label: t(`runtime.agentDaemon.status.${runtime.liveness}`, { defaultValue: runtime.liveness }),
  }
}

/** How many prompts the daemon says it is serving right now, when it says. */
function daemonActiveRequests(runtime: Runtime): number | null {
  const raw = (runtime.config ?? {}).agent_daemon_active_requests
  if (typeof raw === "number" && Number.isFinite(raw)) return Math.max(0, Math.trunc(raw))
  if (typeof raw === "string" && raw.trim() !== "") {
    const parsed = Number(raw)
    if (Number.isFinite(parsed)) return Math.max(0, Math.trunc(parsed))
  }
  return null
}

/** "Claude Code 1.0.112 · Codex 0.42.0"; kinds that are not detected are left out. */
function formatAgentKinds(runtime: Runtime): string {
  return supportedAgentKinds(runtime)
    .filter((kind) => kind.available)
    .map((kind) => {
      const label = formatAgentKindLabel(kind.kind)
      return kind.version ? `${label} ${kind.version}` : label
    })
    .join(" · ")
}
