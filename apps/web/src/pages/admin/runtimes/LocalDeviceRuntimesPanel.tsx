import { useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { Loader2, ShieldAlert, Trash2 } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { ErrorState } from "../../../components/ui/error-state"
import { PairDaemonDialog } from "../../../components/admin/PairDaemonDialog"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../../components/ui/table"
import {
  isLocalDeviceRuntime,
  supportedAgentKinds,
  useDeleteRuntime,
  useWorkspaceRuntimes,
  type Runtime,
  type SupportedAgentKind,
} from "../../../lib/api-runtimes"
import { useWorkspaceId } from "../../../lib/workspace"

export function LocalDeviceRuntimesPanel() {
  const workspaceID = useWorkspaceId()
  // Parent only mounts when a workspace is resolved; guard anyway.
  if (!workspaceID) return null
  return <LocalDeviceRuntimesPanelInner workspaceID={workspaceID} />
}

function LocalDeviceRuntimesPanelInner({ workspaceID }: { workspaceID: string }) {
  const { t } = useTranslation("admin")
  const listQ = useWorkspaceRuntimes(workspaceID, "agent_daemon")
  const deleteMut = useDeleteRuntime(workspaceID)
  const [pairOpen, setPairOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)

  if (listQ.isLoading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
      </div>
    )
  }
  if (listQ.error) {
    return (
      <ErrorState
        title={t("runtime.agentDaemon.errors.loadFailed", {
          defaultValue: "Failed to load local devices",
        })}
        description={(listQ.error as Error).message}
        onRetry={() => void listQ.refetch()}
      />
    )
  }

  const runtimes = (listQ.data ?? []).filter(
    (r) => isLocalDeviceRuntime(r) && r.liveness !== "pending_pairing",
  )
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-sm text-fg-subtle">
          {t("runtime.agentDaemon.intro", {
            defaultValue:
              "Local devices dial back to Parsar through parsar-daemon, letting Agents use the Claude Code / OpenCode CLIs installed on this machine.",
          })}
        </p>
        <Button
          size="sm"
          variant="default"
          onClick={() => setPairOpen(true)}
          data-testid="agent-daemon-pair-button"
        >
          {t("runtime.agentDaemon.actions.pair", { defaultValue: "Pair a new device" })}
        </Button>
      </div>

      {runtimes.length === 0 ? (
        <div className="rounded-md border border-dashed border-line bg-surface-subtle/60 p-6 text-center text-sm text-fg-subtle">
          {t("runtime.agentDaemon.empty", {
            defaultValue: "No local devices paired yet. Use the button above to generate a pairing command.",
          })}
        </div>
      ) : (
        <div className="rounded-lg border border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("runtime.agentDaemon.table.name", { defaultValue: "Name" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.hostname", { defaultValue: "Hostname" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.version", { defaultValue: "Version" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.agentEngines", { defaultValue: "Agent engines" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.status", { defaultValue: "Status" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.heartbeat", { defaultValue: "Last heartbeat" })}</TableHead>
                <TableHead className="w-[60px]" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {runtimes.map((r) => (
                <DaemonRuntimeRow
                  key={r.id}
                  runtime={r}
                  onDelete={() => setDeleteTarget({ id: r.id, name: r.name })}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <PairDaemonDialog
        open={pairOpen}
        onClose={() => setPairOpen(false)}
        workspaceID={workspaceID}
      />

      {deleteTarget && (
        <ConfirmDeleteRuntimeDialog
          targetName={deleteTarget.name}
          pending={deleteMut.isPending}
          error={deleteMut.error as Error | undefined}
          onCancel={() => { setDeleteTarget(null); deleteMut.reset() }}
          onConfirm={() => {
            deleteMut.mutate(deleteTarget.id, {
              onSuccess: () => setDeleteTarget(null),
            })
          }}
        />
      )}
    </div>
  )
}

function DaemonRuntimeRow({
  runtime,
  onDelete,
}: {
  runtime: Runtime
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const activeRequests = daemonActiveRequests(runtime)
  const lastHeartbeatExact = runtime.last_heartbeat_at ? new Date(runtime.last_heartbeat_at).toLocaleString() : null

  return (
    <TableRow data-testid={`agent-daemon-row-${runtime.id}`}>
      <TableCell className="font-mono text-sm text-fg-emphasis">
        <div className="space-y-1">
          <div>{runtime.name}</div>
          <div className="text-xs text-fg-subtle">{shortRuntimeID(runtime.id)}</div>
        </div>
      </TableCell>
      <TableCell className="text-sm text-fg-muted">{runtime.hostname || "—"}</TableCell>
      <TableCell className="text-sm text-fg-muted">
        <div className="space-y-1">
          <div>{runtime.version || "—"}</div>
          {activeRequests !== null && (
            <div className="text-xs text-fg-subtle">
              {activeRequests === 0
                ? t("runtime.agentDaemon.load.idle", { defaultValue: "Idle" })
                : t("runtime.agentDaemon.load.active", { count: activeRequests, defaultValue: "{{count}} running" })}
            </div>
          )}
        </div>
      </TableCell>
      <TableCell>
        <AgentKindBadges runtime={runtime} />
      </TableCell>
      <TableCell>
        <div className="space-y-1">
          <DaemonStatusBadge liveness={runtime.liveness} />
          <p className="max-w-[240px] text-xs leading-4 text-fg-subtle">{daemonStatusDetail(runtime, t)}</p>
        </div>
      </TableCell>
      <TableCell className="text-sm text-fg-muted" title={lastHeartbeatExact ?? undefined}>
        {runtime.last_heartbeat_at ? (
          <div className="space-y-1">
            <div>{relativeAgo(runtime.last_heartbeat_at)}</div>
            <div className="text-xs text-fg-subtle">{lastHeartbeatExact}</div>
          </div>
        ) : (
          "—"
        )}
      </TableCell>
      <TableCell>
        <button
          type="button"
          onClick={onDelete}
          className="inline-flex h-7 w-7 items-center justify-center rounded text-fg-faint hover:bg-danger-subtle hover:text-danger"
          title={t("runtime.agentDaemon.actions.delete", { defaultValue: "Delete device" })}
          data-testid={`agent-daemon-delete-${runtime.id}`}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </TableCell>
    </TableRow>
  )
}

function AgentKindBadges({ runtime }: { runtime: Runtime }) {
  const { t } = useTranslation("admin")
  const kinds = supportedAgentKinds(runtime)
  const capabilities = daemonCapabilityLabels(runtime, t)
  if (kinds.length === 0) {
    return <span className="text-sm text-fg-faint">—</span>
  }
  return (
    <div className="space-y-1.5">
      <div className="flex max-w-[240px] flex-wrap gap-1">
        {kinds.map((kind) => (
          <Badge
            key={`${kind.kind}-${kind.available ? "on" : "off"}`}
            variant={kind.available ? "primary" : "neutral"}
            className={kind.available ? "" : "opacity-70"}
            title={formatAgentKindTitle(kind, t)}
          >
            {formatAgentKindLabel(kind.kind)}
          </Badge>
        ))}
      </div>
      <div className="text-xs leading-4 text-fg-subtle">
        {kinds.map((kind) => formatAgentKindSnapshot(kind, t)).join(" · ")}
      </div>
      {capabilities.length > 0 && (
        <div className="text-xs leading-4 text-fg-subtle">{capabilities.join(" · ")}</div>
      )}
    </div>
  )
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

function formatAgentKindTitle(kind: SupportedAgentKind, t: TFunction<"admin">): string {
  const parts = [
    formatAgentKindLabel(kind.kind),
    kind.available
      ? t("runtime.agentDaemon.agentKind.available", { defaultValue: "available" })
      : t("runtime.agentDaemon.agentKind.unavailable", { defaultValue: "unavailable" }),
  ]
  if (kind.version) parts.push(kind.version)
  return parts.join(" · ")
}

function formatAgentKindSnapshot(kind: SupportedAgentKind, t: TFunction<"admin">): string {
  const label = formatAgentKindLabel(kind.kind)
  if (!kind.available) {
    return t("runtime.agentDaemon.agentKind.notDetected", { label, defaultValue: "{{label}} not detected" })
  }
  if (kind.version) {
    return t("runtime.agentDaemon.agentKind.version", { label, version: kind.version, defaultValue: "{{label}} {{version}}" })
  }
  return t("runtime.agentDaemon.agentKind.usable", { label, defaultValue: "{{label}} available" })
}

function DaemonStatusBadge({
  liveness,
}: {
  liveness: Runtime["liveness"]
}) {
  const { t } = useTranslation("admin")
  switch (liveness) {
    case "online":
      return (
        <Badge variant="success" dot>
          {t("runtime.agentDaemon.status.online", { defaultValue: "Online" })}
        </Badge>
      )
    case "pending_pairing":
      return (
        <Badge variant="warning">
          {t("runtime.agentDaemon.status.pending_pairing", { defaultValue: "Pairing" })}
        </Badge>
      )
    case "error":
      return (
        <Badge variant="destructive">
          {t("runtime.agentDaemon.status.error", { defaultValue: "Error" })}
        </Badge>
      )
    default:
      return (
        <Badge variant="neutral">
          {t("runtime.agentDaemon.status.offline", { defaultValue: "Offline" })}
        </Badge>
      )
  }
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

function daemonCapabilityLabels(runtime: Runtime, t: TFunction<"admin">): string[] {
  const raw = runtime.config.daemon_capabilities
  if (!isRecord(raw)) return []
  const out: string[] = []
  if (raw.streaming === true) out.push(t("runtime.agentDaemon.capabilities.streaming", { defaultValue: "Streaming" }))
  if (raw.permissions === true) out.push(t("runtime.agentDaemon.capabilities.permissions", { defaultValue: "Permissions" }))
  if (raw.usage === true) out.push(t("runtime.agentDaemon.capabilities.usage", { defaultValue: "Usage" }))
  if (raw.resume === true) out.push(t("runtime.agentDaemon.capabilities.resume", { defaultValue: "Resume" }))
  return out
}

function daemonStatusDetail(runtime: Runtime, t: TFunction<"admin">): string {
  const activeRequests = daemonActiveRequests(runtime) ?? 0
  const kinds = supportedAgentKinds(runtime)
  const availableKinds = kinds.filter((kind) => kind.available)
  switch (runtime.liveness) {
    case "pending_pairing":
      return t("runtime.agentDaemon.detail.pendingPairing", { defaultValue: "Waiting for parsar-daemon connect to complete pairing." })
    case "offline":
      return kinds.length === 0
        ? t("runtime.agentDaemon.detail.offlineNoSnapshot", { defaultValue: "No capability snapshot yet — make sure parsar-daemon is connected on the device." })
        : t("runtime.agentDaemon.detail.offlineWithSnapshot", { defaultValue: "Recent heartbeats lost — run parsar-daemon status or parsar-daemon logs on the device to diagnose." })
    case "error":
      return t("runtime.agentDaemon.detail.error", { defaultValue: "Daemon reported an error — run parsar-daemon logs on the device to inspect." })
    case "online":
      if (kinds.length > 0 && availableKinds.length === 0) {
        return t("runtime.agentDaemon.detail.onlineNoCli", { defaultValue: "WebSocket connected, but no Agent CLI is installed on this host." })
      }
      if (activeRequests > 0) {
        return t("runtime.agentDaemon.detail.onlineActive", { count: activeRequests, defaultValue: "{{count}} run(s) currently executing." })
      }
      return t("runtime.agentDaemon.detail.onlineIdle", { defaultValue: "Online, waiting for new requests." })
    default:
      return "—"
  }
}

function shortRuntimeID(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
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
  return (
    <Dialog open onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5">
          <div className="shrink-0 rounded-full bg-danger-subtle p-2 text-danger-emphasis">
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">
              {t("runtime.agentDaemon.delete.title", {
                name: targetName,
                defaultValue: "Delete device {{name}}",
              })}
            </DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {t("runtime.agentDaemon.delete.description", {
                defaultValue: "Once deleted, this device can no longer accept new tasks; running tasks are unaffected. This action cannot be undone.",
              })}
            </DialogDescription>
            {error && (
              <p className="text-sm text-danger-emphasis">{error.message}</p>
            )}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-line-muted bg-surface-subtle/60 px-4 py-3">
          <Button
            variant="outline"
            size="sm"
            onClick={onCancel}
            disabled={pending}
          >
            {t("common.actions.cancel", { defaultValue: "Cancel" })}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            disabled={pending}
          >
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("runtime.agentDaemon.delete.confirm", { defaultValue: "Delete" })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function relativeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (ms < 0 || Number.isNaN(ms)) return iso
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 48) return `${hr}h ago`
  return `${Math.floor(hr / 24)}d ago`
}
