import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Box, CalendarClock, Loader2, RotateCcw } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { ErrorState } from "../ui/error-state"
import { PropertyList, Property } from "../ui/property-list"
import { Skeleton } from "../ui/skeleton"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import {
  useSandboxBinding,
  useRebuildSandbox,
  useAcquireSandbox,
  useRenewSandbox,
  type SandboxStatusKind,
} from "../../lib/api-sandbox"
import { useWorkspaceRuntimes } from "../../lib/api-runtimes"
import { findSandboxRuntimeForAgent, isSandboxPairingExpired } from "../../lib/sandbox-runtime"
import { useNow } from "../../lib/use-now"
import { useRelativeTime } from "../../lib/relative-time"
import { SandboxPreparingNotice, SandboxStartupTimedOutNotice } from "./SandboxProvisioningNotice"

const STATUS_FOR_KIND: Record<SandboxStatusKind, StatusKind> = {
  live: "completed",
  transient: "running",
  terminal: "cancelled",
}

function Section({
  title,
  action,
  children,
}: {
  title: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section>
      <div className="flex h-7 items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-fg">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  )
}

function Timestamp({ iso }: { iso: string }) {
  // Subscribe to the ticking clock so "Xm ago" advances; the value itself is unused.
  useNow()
  const fmtAgo = useRelativeTime()
  return <span title={iso}>{fmtAgo(iso)}</span>
}

function describeRemaining(iso: string, now: number): string | null {
  const target = new Date(iso).getTime()
  if (Number.isNaN(target)) return null
  const ms = target - now
  if (ms <= 0) return ""
  const totalMinutes = Math.floor(ms / 60_000)
  const days = Math.floor(totalMinutes / (60 * 24))
  const hours = Math.floor((totalMinutes - days * 60 * 24) / 60)
  const minutes = totalMinutes - days * 60 * 24 - hours * 60
  if (days >= 1) return hours > 0 ? `${days}d ${hours}h` : `${days}d`
  if (hours >= 1) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`
  return `${Math.max(minutes, 1)}m`
}

function ExpiresValue({ iso }: { iso?: string }) {
  const { t } = useTranslation("admin")
  const now = useNow()
  if (!iso) return <>{t("agents.detail.sandbox.fields.expiresAtUnknown")}</>
  const remaining = describeRemaining(iso, now)
  const label =
    remaining === ""
      ? t("agents.detail.sandbox.expires.expired")
      : remaining
        ? t("agents.detail.sandbox.expires.remaining", { value: remaining })
        : null
  return (
    <span title={iso} className="truncate">
      {new Date(iso).toLocaleString()}
      {label && <span className="text-fg-muted"> · {label}</span>}
    </span>
  )
}

export function SandboxPanel({
  workspaceID,
  agentID,
}: {
  workspaceID: string | null
  agentID: string
}) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const query = useSandboxBinding(workspaceID, agentID)
  const runtimeQuery = useWorkspaceRuntimes(workspaceID ?? "", "agent_daemon")
  const rebuildMut = useRebuildSandbox(workspaceID, agentID)
  const acquireMut = useAcquireSandbox(workspaceID, agentID)
  const renewMut = useRenewSandbox(workspaceID, agentID)
  const now = useNow()
  const refetchSandbox = query.refetch
  const refetchRuntimes = runtimeQuery.refetch

  const [confirmingRebuild, setConfirmingRebuild] = useState(false)
  const [provisioningSince, setProvisioningSince] = useState<number | null>(null)

  function handleConfirm() {
    setProvisioningSince(Date.now())
    rebuildMut.mutate(undefined, { onSettled: () => setConfirmingRebuild(false) })
  }

  function triggerAcquire() {
    setProvisioningSince(Date.now())
    acquireMut.mutate()
  }

  const binding = query.data
  const sandboxRuntime = findSandboxRuntimeForAgent(runtimeQuery.data ?? [], agentID)
  const runtimeTimedOut = sandboxRuntime ? isSandboxPairingExpired(sandboxRuntime, now) : false
  const manualProvisioningActive =
    provisioningSince !== null &&
    now - provisioningSince < 5 * 60_000 &&
    (!binding || binding.status_kind !== "live")
  const preparing =
    acquireMut.isPending ||
    rebuildMut.isPending ||
    manualProvisioningActive ||
    Boolean(sandboxRuntime?.liveness === "pending_pairing" && !runtimeTimedOut)

  useEffect(() => {
    if (!preparing) return
    const tick = window.setInterval(() => {
      void refetchSandbox()
      void refetchRuntimes()
    }, 2500)
    return () => window.clearInterval(tick)
  }, [preparing, refetchSandbox, refetchRuntimes])

  if (query.isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-3 w-1/3" />
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
  if (!binding) {
    return (
      <Section title={t("agents.detail.sandbox.title")}>
        {preparing ? (
          <SandboxPreparingNotice
            runtime={sandboxRuntime}
            startedAt={
              sandboxRuntime?.created_at ??
              (provisioningSince ? new Date(provisioningSince).toISOString() : undefined)
            }
          />
        ) : sandboxRuntime && runtimeTimedOut ? (
          <SandboxStartupTimedOutNotice
            runtime={sandboxRuntime}
            retrying={acquireMut.isPending}
            onRetry={triggerAcquire}
          />
        ) : (
          <EmptyState
            icon={Box}
            title={t("agents.detail.sandbox.empty.title")}
            className="py-8"
            action={
              <Button size="sm" variant="outline" disabled={acquireMut.isPending} onClick={triggerAcquire}>
                {acquireMut.isPending && <Loader2 className="animate-spin" />}
                {t("agents.detail.sandbox.actions.provision")}
              </Button>
            }
          />
        )}
        {acquireMut.error && (
          <ErrorState
            title={t("agents.detail.sandbox.provisionError")}
            description={(acquireMut.error as Error).message}
          />
        )}
      </Section>
    )
  }

  const busy = renewMut.isPending || rebuildMut.isPending || binding.status_kind !== "live"

  return (
    <div className="space-y-4">
      <Section
        title={t("agents.detail.sandbox.title")}
        action={
          <div className="flex items-center gap-1">
            <Button size="sm" variant="outline" data-testid="sandbox-renew-button" disabled={busy} onClick={() => renewMut.mutate()}>
              <CalendarClock strokeWidth={1.5} aria-hidden="true" />
              {renewMut.isPending
                ? t("agents.detail.sandbox.actions.renewing")
                : t("agents.detail.sandbox.actions.renew")}
            </Button>
            <Button size="sm" variant="outline" data-testid="sandbox-rebuild-button" disabled={busy} onClick={() => setConfirmingRebuild(true)}>
              <RotateCcw strokeWidth={1.5} aria-hidden="true" />
              {rebuildMut.isPending
                ? t("agents.detail.sandbox.actions.rebuilding")
                : t("agents.detail.sandbox.actions.rebuild")}
            </Button>
          </div>
        }
      >
        <PropertyList>
          <Property label={t("runtime.detail.fields.status")}>
            <StatusIcon status={STATUS_FOR_KIND[binding.status_kind]} />
            <span className="truncate">{binding.status}</span>
          </Property>
          <Property label={t("agents.detail.sandbox.fields.sandboxId")} mono>{binding.sandbox_id}</Property>
          <Property label={t("agents.detail.sandbox.fields.templateId")} mono>{binding.template_id}</Property>
          <Property label={t("agents.detail.sandbox.fields.expiresAt")} mono>
            <ExpiresValue iso={binding.expires_at} />
          </Property>
          <Property label={t("agents.detail.sandbox.fields.lastActive")}>
            <Timestamp iso={binding.last_active_at} />
          </Property>
          <Property label={t("agents.detail.sandbox.fields.createdAt")}>
            <Timestamp iso={binding.created_at} />
          </Property>
          {binding.killed_at && (
            <Property label={t("agents.detail.sandbox.fields.killedAt")}>
              <Timestamp iso={binding.killed_at} />
            </Property>
          )}
          <Property label={t("agents.detail.sandbox.fields.bindingId")} mono>{binding.binding_id}</Property>
          <Property label={t("agents.detail.sandbox.fields.cacheKey")} mono>{binding.cache_key}</Property>
        </PropertyList>
        {binding.status_kind !== "live" && (
          <p className="mt-2 text-xs text-fg-muted">{t("agents.detail.sandbox.notLiveHint")}</p>
        )}
        {preparing && (
          <div className="mt-3 border-t border-line">
            <SandboxPreparingNotice runtime={sandboxRuntime} />
          </div>
        )}
      </Section>

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
        <p className="flex h-8 items-center gap-2 border-t border-line text-sm text-fg" role="status">
          <StatusIcon status="completed" />
          {t("agents.detail.sandbox.renewedToast", {
            expiresAt: new Date(renewMut.data.expires_at).toLocaleString(),
          })}
        </p>
      )}

      <AlertDialog
        open={confirmingRebuild}
        onOpenChange={(next) => {
          if (!next && !rebuildMut.isPending) setConfirmingRebuild(false)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("agents.detail.sandbox.confirm.rebuild.title")}</AlertDialogTitle>
            <AlertDialogDescription>{t("agents.detail.sandbox.confirm.rebuild.description")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" disabled={rebuildMut.isPending}>{tc("actions.cancel")}</Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                onClick={(e) => {
                  e.preventDefault()
                  handleConfirm()
                }}
                disabled={rebuildMut.isPending}
                data-testid="sandbox-confirm-button"
              >
                {rebuildMut.isPending && <Loader2 className="animate-spin" />}
                {t("agents.detail.sandbox.confirm.rebuild.confirmLabel")}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
