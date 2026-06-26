import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Box, CalendarClock, Loader2, RotateCcw, ShieldAlert } from "lucide-react"

import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { EmptyState } from "../ui/empty-state"
import { ErrorState } from "../ui/error-state"
import { Skeleton } from "../ui/skeleton"
import { useSandboxBinding, useRebuildSandbox, useAcquireSandbox, useRenewSandbox, type SandboxStatusKind } from "../../lib/api-sandbox"
import { useNow } from "../../lib/use-now"

function Card({ title, className, children }: { title: string; className?: string; children: React.ReactNode }) {
  return (
    <section className={`rounded-lg border border-slate-200 bg-white p-4 ${className ?? ""}`}>
      <h3 className="mb-3 text-[12px] font-semibold uppercase tracking-wider text-slate-500">{title}</h3>
      {children}
    </section>
  )
}

function Field({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="mb-2 last:mb-0">
      <dt className="mb-0.5 text-[11px] uppercase tracking-wider text-slate-400">{label}</dt>
      <dd className={`text-[13px] text-slate-800 ${mono ? "font-mono break-all" : ""}`}>{value}</dd>
    </div>
  )
}

function SandboxStatusBadge({ kind, status }: { kind: SandboxStatusKind; status: string }) {
  if (kind === "live") return <Badge variant="success" dot>{status}</Badge>
  if (kind === "transient") return <Badge variant="warning" dot>{status}</Badge>
  return <Badge variant="neutral">{status}</Badge>
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
  const day = Math.floor(hr / 24)
  return `${day}d ago`
}

function Timestamp({ iso }: { iso: string }) {
  // Subscribe to the ticking clock so "Xm ago" advances; the value itself is unused.
  useNow()
  return (
    <span title={iso} className="text-[13px] text-slate-800">
      {relativeAgo(iso)}
    </span>
  )
}

// Tone thresholds: green > 7d, amber 1–7d, red < 1d.
interface RemainingDescriptor {
  /** Pre-rendered duration for the i18n `{{value}}` slot. */
  value: string
  tone: "green" | "amber" | "red"
  state: "expired" | "remaining"
}

function describeRemaining(iso: string | undefined, now: number): RemainingDescriptor | null {
  if (!iso) return null
  const target = new Date(iso).getTime()
  if (Number.isNaN(target)) return null
  const ms = target - now
  if (ms <= 0) return { value: "", tone: "red", state: "expired" }
  const totalMinutes = Math.floor(ms / 60_000)
  const days = Math.floor(totalMinutes / (60 * 24))
  const hours = Math.floor((totalMinutes - days * 60 * 24) / 60)
  const minutes = totalMinutes - days * 60 * 24 - hours * 60
  let value: string
  if (days >= 1) value = hours > 0 ? `${days} 天 ${hours} 小时` : `${days} 天`
  else if (hours >= 1) value = minutes > 0 ? `${hours} 小时 ${minutes} 分` : `${hours} 小时`
  else value = `${Math.max(minutes, 1)} 分钟`
  const tone: RemainingDescriptor["tone"] = days < 1 ? "red" : days < 7 ? "amber" : "green"
  return { value, tone, state: "remaining" }
}

function ExpiresValue({ iso }: { iso?: string }) {
  const { t } = useTranslation("admin")
  const now = useNow()
  if (!iso) {
    return <span className="text-[13px] text-slate-500">{t("agents.detail.sandbox.fields.expiresAtUnknown")}</span>
  }
  const desc = describeRemaining(iso, now)
  const toneClass =
    desc?.tone === "red" ? "text-red-600"
    : desc?.tone === "amber" ? "text-amber-600"
    : "text-emerald-700"
  const absolute = new Date(iso).toLocaleString()
  const label = desc?.state === "expired"
    ? t("agents.detail.sandbox.expires.expired")
    : desc
      ? t("agents.detail.sandbox.expires.remaining", { value: desc.value })
      : null
  return (
    <span title={iso} className="text-[13px] text-slate-800">
      {absolute}
      {label && <span className={`ml-2 text-[12px] ${toneClass}`}>({label})</span>}
    </span>
  )
}

interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
  loading?: boolean
}

function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
  loading,
}: ConfirmDialogProps) {
  const { t } = useTranslation("common")
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !loading) onCancel() }}>
      <DialogContent showCloseButton={false} className="max-w-md gap-0 p-0">
        <DialogHeader className="flex flex-row items-start gap-3 space-y-0 p-5 pr-5">
          <div
            className={
              destructive
                ? "shrink-0 rounded-full bg-red-100 p-2 text-red-700"
                : "shrink-0 rounded-full bg-amber-100 p-2 text-amber-700"
            }
          >
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="space-y-1.5">
            <DialogTitle className="text-sm">{title}</DialogTitle>
            <DialogDescription className="text-sm leading-relaxed">
              {description}
            </DialogDescription>
          </div>
        </DialogHeader>
        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
          <Button variant="outline" size="sm" onClick={onCancel} disabled={loading}>
            {t("actions.cancel")}
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            size="sm"
            onClick={onConfirm}
            disabled={loading}
            data-testid="sandbox-confirm-button"
          >
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {confirmLabel ?? t("actions.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function SandboxPanel({
  workspaceID,
  projectAgentID,
}: {
  workspaceID: string | null
  projectAgentID: string
}) {
  const { t } = useTranslation("admin")
  const query = useSandboxBinding(workspaceID, projectAgentID)
  const rebuildMut = useRebuildSandbox(workspaceID, projectAgentID)
  const acquireMut = useAcquireSandbox(workspaceID, projectAgentID)
  const renewMut = useRenewSandbox(workspaceID, projectAgentID)

  const [confirmingRebuild, setConfirmingRebuild] = useState(false)

  function handleConfirm() {
    rebuildMut.mutate(undefined, { onSettled: () => setConfirmingRebuild(false) })
  }

  if (query.isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-24" />
      </div>
    )
  }

  if (query.error) {
    return (
      <ErrorState
        title={t("agents.detail.sandbox.errorTitle")}
        description={(query.error as Error).message}
        onRetry={() => void query.refetch()}
      />
    )
  }

  const binding = query.data

  if (!binding) {
    return (
      <Card title={t("agents.detail.sandbox.title")}>
        <EmptyState
          icon={Box}
          title={t("agents.detail.sandbox.empty.title")}
        />
        <div className="mt-3 flex justify-center">
          <Button
            size="sm"
            variant="outline"
            disabled={acquireMut.isPending}
            onClick={() => acquireMut.mutate()}
          >
            {acquireMut.isPending && <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />}
            {t("agents.detail.sandbox.actions.provision")}
          </Button>
        </div>
        {acquireMut.isSuccess && (
          <p className="mt-2 text-center text-[12px] text-slate-500">
            {t("agents.detail.sandbox.provisioningHint")}
          </p>
        )}
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card title={t("agents.detail.sandbox.title")}>
        <div className="mb-4 flex items-center justify-between">
          <SandboxStatusBadge kind={binding.status_kind} status={binding.status} />
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              data-testid="sandbox-renew-button"
              disabled={renewMut.isPending || rebuildMut.isPending || binding.status_kind !== "live"}
              onClick={() => renewMut.mutate()}
            >
              <CalendarClock className="mr-1 h-3.5 w-3.5" strokeWidth={2} />
              {renewMut.isPending
                ? t("agents.detail.sandbox.actions.renewing")
                : t("agents.detail.sandbox.actions.renew")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              data-testid="sandbox-rebuild-button"
              disabled={rebuildMut.isPending || renewMut.isPending || binding.status_kind !== "live"}
              onClick={() => setConfirmingRebuild(true)}
            >
              <RotateCcw className="mr-1 h-3.5 w-3.5" strokeWidth={2} />
              {rebuildMut.isPending
                ? t("agents.detail.sandbox.actions.rebuilding")
                : t("agents.detail.sandbox.actions.rebuild")}
            </Button>
          </div>
        </div>
        <dl>
          <Field label={t("agents.detail.sandbox.fields.sandboxId")} value={binding.sandbox_id} mono />
          <Field label={t("agents.detail.sandbox.fields.templateId")} value={binding.template_id} mono />
          <Field label={t("agents.detail.sandbox.fields.expiresAt")} value={<ExpiresValue iso={binding.expires_at} />} />
          <Field label={t("agents.detail.sandbox.fields.lastActive")} value={<Timestamp iso={binding.last_active_at} />} />
          <Field label={t("agents.detail.sandbox.fields.createdAt")} value={<Timestamp iso={binding.created_at} />} />
          {binding.killed_at && (
            <Field label={t("agents.detail.sandbox.fields.killedAt")} value={<Timestamp iso={binding.killed_at} />} />
          )}
          <Field label={t("agents.detail.sandbox.fields.bindingId")} value={binding.binding_id} mono />
          <Field label={t("agents.detail.sandbox.fields.cacheKey")} value={binding.cache_key} mono />
        </dl>
        {binding.status_kind !== "live" && (
          <p className="mt-3 rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-[12px] text-slate-600">
            {t("agents.detail.sandbox.notLiveHint")}
          </p>
        )}
      </Card>

      {rebuildMut.error && (
        <ErrorState
          title={t("agents.detail.sandbox.rebuildError")}
          description={(rebuildMut.error as Error).message}
        />
      )}
      {renewMut.error && (
        <ErrorState
          title={t("agents.detail.sandbox.renewError")}
          description={(renewMut.error as Error).message}
        />
      )}
      {renewMut.isSuccess && renewMut.data?.expires_at && (
        <div className="rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-[12px] text-emerald-800">
          {t("agents.detail.sandbox.renewedToast", { expiresAt: new Date(renewMut.data.expires_at).toLocaleString() })}
        </div>
      )}

      <ConfirmDialog
        open={confirmingRebuild}
        title={t("agents.detail.sandbox.confirm.rebuild.title")}
        description={t("agents.detail.sandbox.confirm.rebuild.description")}
        confirmLabel={t("agents.detail.sandbox.confirm.rebuild.confirmLabel")}
        loading={rebuildMut.isPending}
        onCancel={() => setConfirmingRebuild(false)}
        onConfirm={handleConfirm}
      />
    </div>
  )
}
