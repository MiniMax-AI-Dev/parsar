import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { RefreshCw } from "lucide-react"

import { Skeleton } from "../ui/skeleton"
import { Button } from "../ui/button"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import { useRuntimeStatus, type RuntimeStatus } from "../../lib/api-runtime"

interface RuntimeStatusBannerProps {
  workspaceID: string | null
  /** One action for the row's right edge (e.g. the credential button). */
  action?: ReactNode
  className?: string
}

/**
 * Cloud runtime status as a 32px hairline row: a 14px status icon, the
 * status sentence in ink, and at most one action on the right.
 */
export function RuntimeStatusBanner({ workspaceID, action, className }: RuntimeStatusBannerProps) {
  const { t } = useTranslation("admin")
  const query = useRuntimeStatus(workspaceID)

  if (query.isLoading) {
    return (
      <div className={className} data-testid="runtime-status-banner-loading">
        <div className="flex h-8 items-center border-b border-line">
          <Skeleton className="h-3 w-56" />
        </div>
      </div>
    )
  }

  if (query.error || !query.data) {
    return (
      <StatusRow
        status="failed"
        title={t("runtime.status.unreachable")}
        role="alert"
        testId="runtime-status-banner-warn"
        className={className}
        action={
          <Button size="sm" variant="outline" onClick={() => void query.refetch()} data-testid="runtime-status-retry">
            <RefreshCw strokeWidth={1.5} aria-hidden="true" />
            {t("runtime.status.retry")}
          </Button>
        }
      />
    )
  }

  const copy = describeStatus(query.data)
  return (
    <StatusRow
      status={copy.status}
      title={t(copy.titleKey)}
      role={copy.status === "interrupted" ? "alert" : "status"}
      testId={`runtime-status-banner-${copy.shape}`}
      className={className}
      action={action}
    />
  )
}

type StatusCopyKey =
  | "runtime.status.cloudReady"
  | "runtime.status.cloudOff"
  | "runtime.status.cloudMisconfigured"
  | "runtime.status.cloudRunnerUnavailable"

type Shape = "ok" | "info" | "warn"

interface BannerKeys {
  shape: Shape
  status: StatusKind
  titleKey: StatusCopyKey
}

function describeStatus(s: RuntimeStatus): BannerKeys {
  // Managed: Parsar owns the credential; admins never see the missing-cred path.
  if (s.profile === "managed") {
    return s.available
      ? { shape: "ok", status: "completed", titleKey: "runtime.status.cloudReady" }
      : { shape: "warn", status: "interrupted", titleKey: "runtime.status.cloudMisconfigured" }
  }
  if (s.has_credential) {
    return s.available
      ? { shape: "ok", status: "completed", titleKey: "runtime.status.cloudReady" }
      : { shape: "warn", status: "interrupted", titleKey: "runtime.status.cloudRunnerUnavailable" }
  }
  // No credential: blocking when sandbox agents already exist, otherwise opt-in.
  if (s.sandbox_agent_count > 0) {
    return { shape: "warn", status: "interrupted", titleKey: "runtime.status.cloudMisconfigured" }
  }
  return { shape: "info", status: "queued", titleKey: "runtime.status.cloudOff" }
}

function StatusRow({
  status,
  title,
  role,
  testId,
  action,
  className,
}: {
  status: StatusKind
  title: string
  role: "status" | "alert"
  testId: string
  action?: ReactNode
  className?: string
}) {
  return (
    <div
      className={`flex h-8 items-center gap-2 border-b border-line text-sm text-fg ${className ?? ""}`}
      role={role}
      data-testid={testId}
    >
      <StatusIcon status={status} />
      <span className="min-w-0 flex-1 truncate">{title}</span>
      {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
    </div>
  )
}
