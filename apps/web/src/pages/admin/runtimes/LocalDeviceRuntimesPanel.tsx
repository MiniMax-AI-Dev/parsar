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
          defaultValue: "无法加载本地设备列表",
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
        <p className="text-[12px] text-slate-500">
          {t("runtime.agentDaemon.intro", {
            defaultValue:
              "本地设备通过 parsar-daemon 反向连接 Parsar，让 Agent 使用这台机器上的 Claude Code / OpenCode 等 CLI。",
          })}
        </p>
        <Button
          size="sm"
          variant="default"
          onClick={() => setPairOpen(true)}
          data-testid="agent-daemon-pair-button"
        >
          {t("runtime.agentDaemon.actions.pair", { defaultValue: "接入新设备" })}
        </Button>
      </div>

      {runtimes.length === 0 ? (
        <div className="rounded-md border border-dashed border-slate-200 bg-slate-50/60 p-6 text-center text-[13px] text-slate-500">
          {t("runtime.agentDaemon.empty", {
            defaultValue: "尚未接入任何本地设备。点击右上角按钮生成连接命令。",
          })}
        </div>
      ) : (
        <div className="rounded-lg border border-slate-200">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("runtime.agentDaemon.table.name", { defaultValue: "名称" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.hostname", { defaultValue: "主机名" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.version", { defaultValue: "版本" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.agentEngines", { defaultValue: "Agent 引擎" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.status", { defaultValue: "状态" })}</TableHead>
                <TableHead>{t("runtime.agentDaemon.table.heartbeat", { defaultValue: "最后心跳" })}</TableHead>
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
      <TableCell className="font-mono text-[12px] text-slate-800">
        <div className="space-y-1">
          <div>{runtime.name}</div>
          <div className="text-[11px] text-slate-500">{shortRuntimeID(runtime.id)}</div>
        </div>
      </TableCell>
      <TableCell className="text-[12px] text-slate-600">{runtime.hostname || "—"}</TableCell>
      <TableCell className="text-[12px] text-slate-600">
        <div className="space-y-1">
          <div>{runtime.version || "—"}</div>
          {activeRequests !== null && (
            <div className="text-[11px] text-slate-500">
              {activeRequests === 0
                ? t("runtime.agentDaemon.load.idle", { defaultValue: "空闲" })
                : t("runtime.agentDaemon.load.active", { count: activeRequests, defaultValue: "{{count}} 个运行中" })}
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
          <p className="max-w-[240px] text-[11px] leading-4 text-slate-500">{daemonStatusDetail(runtime, t)}</p>
        </div>
      </TableCell>
      <TableCell className="text-[12px] text-slate-600" title={lastHeartbeatExact ?? undefined}>
        {runtime.last_heartbeat_at ? (
          <div className="space-y-1">
            <div>{relativeAgo(runtime.last_heartbeat_at)}</div>
            <div className="text-[11px] text-slate-500">{lastHeartbeatExact}</div>
          </div>
        ) : (
          "—"
        )}
      </TableCell>
      <TableCell>
        <button
          type="button"
          onClick={onDelete}
          className="inline-flex h-7 w-7 items-center justify-center rounded text-slate-400 hover:bg-red-50 hover:text-red-600"
          title={t("runtime.agentDaemon.actions.delete", { defaultValue: "删除设备" })}
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
    return <span className="text-[12px] text-slate-400">—</span>
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
      <div className="text-[11px] leading-4 text-slate-500">
        {kinds.map((kind) => formatAgentKindSnapshot(kind, t)).join(" · ")}
      </div>
      {capabilities.length > 0 && (
        <div className="text-[11px] leading-4 text-slate-500">{capabilities.join(" · ")}</div>
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
    case "pi_agent":
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
    return t("runtime.agentDaemon.agentKind.notDetected", { label, defaultValue: "{{label}} 未检测到" })
  }
  if (kind.version) {
    return t("runtime.agentDaemon.agentKind.version", { label, version: kind.version, defaultValue: "{{label}} {{version}}" })
  }
  return t("runtime.agentDaemon.agentKind.usable", { label, defaultValue: "{{label}} 可用" })
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
          {t("runtime.agentDaemon.status.online", { defaultValue: "在线" })}
        </Badge>
      )
    case "pending_pairing":
      return (
        <Badge variant="warning">
          {t("runtime.agentDaemon.status.pending_pairing", { defaultValue: "等待配对" })}
        </Badge>
      )
    case "error":
      return (
        <Badge variant="destructive">
          {t("runtime.agentDaemon.status.error", { defaultValue: "错误" })}
        </Badge>
      )
    default:
      return (
        <Badge variant="neutral">
          {t("runtime.agentDaemon.status.offline", { defaultValue: "离线" })}
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
      return t("runtime.agentDaemon.detail.pendingPairing", { defaultValue: "等待执行 parsar-daemon connect 完成配对。" })
    case "offline":
      return kinds.length === 0
        ? t("runtime.agentDaemon.detail.offlineNoSnapshot", { defaultValue: "还没收到 capability 快照；请先确认设备上的 parsar-daemon 已连上。" })
        : t("runtime.agentDaemon.detail.offlineWithSnapshot", { defaultValue: "最近心跳中断；可在设备上运行 parsar-daemon status 或 parsar-daemon logs 排查。" })
    case "error":
      return t("runtime.agentDaemon.detail.error", { defaultValue: "daemon 上报错误；请在设备上运行 parsar-daemon logs 查看最近错误。" })
    case "online":
      if (kinds.length > 0 && availableKinds.length === 0) {
        return t("runtime.agentDaemon.detail.onlineNoCli", { defaultValue: "WebSocket 在线，但本机没有可用的 Agent CLI。" })
      }
      if (activeRequests > 0) {
        return t("runtime.agentDaemon.detail.onlineActive", { count: activeRequests, defaultValue: "当前有 {{count}} 个运行在执行。" })
      }
      return t("runtime.agentDaemon.detail.onlineIdle", { defaultValue: "在线，等待新请求。" })
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
          <div className="shrink-0 rounded-full bg-red-100 p-2 text-red-700">
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">
              {t("runtime.agentDaemon.delete.title", {
                name: targetName,
                defaultValue: "删除设备「{{name}}」",
              })}
            </DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {t("runtime.agentDaemon.delete.description", {
                defaultValue: "删除后该设备将无法接收新任务，已有运行不受影响。此操作不可撤销。",
              })}
            </DialogDescription>
            {error && (
              <p className="text-[12px] text-red-700">{error.message}</p>
            )}
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
          <Button
            variant="outline"
            size="sm"
            onClick={onCancel}
            disabled={pending}
          >
            {t("common.actions.cancel", { defaultValue: "取消" })}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            disabled={pending}
          >
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("runtime.agentDaemon.delete.confirm", { defaultValue: "删除" })}
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
  if (sec < 60) return `${sec}秒前`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 48) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}
