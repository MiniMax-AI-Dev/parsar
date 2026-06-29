import { useTranslation } from "react-i18next"
import { CheckCircle2, Info, AlertTriangle, RefreshCw } from "lucide-react"

import { Skeleton } from "../ui/skeleton"
import { Button } from "../ui/button"
import { useRuntimeStatus } from "../../lib/api-runtime"

interface RuntimeStatusBannerProps {
  workspaceID: string | null
}

type Shape = "ok" | "info" | "warn"

export function RuntimeStatusBanner({ workspaceID }: RuntimeStatusBannerProps) {
  const { t } = useTranslation("admin")
  const query = useRuntimeStatus(workspaceID)

  if (query.isLoading) {
    return (
      <div className="mb-3" data-testid="runtime-status-banner-loading">
        <Skeleton className="h-10 w-full rounded-md" />
      </div>
    )
  }

  // Soft-warn until status endpoint is reachable.
  if (query.error || !query.data) {
    return (
      <BannerView
        shape="warn"
        title={t("runtime.status.unreachable")}
        action={
          <Button
            size="sm"
            variant="outline"
            onClick={() => void query.refetch()}
            data-testid="runtime-status-retry"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t("runtime.status.retry")}
          </Button>
        }
      />
    )
  }

  const copy = describeStatus(query.data)
  return (
    <BannerView
      shape={copy.shape}
      title={t(copy.titleKey)}
      hint={copy.hintKey ? t(copy.hintKey) : undefined}
    />
  )
}

// Returns i18n keys (not strings) so the typed TFunction at the call site
// can resolve them without weakening the key-union type to `string`.
type StatusCopyKey =
  | "runtime.status.cloudReady"
  | "runtime.status.cloudReadyHint"
  | "runtime.status.cloudReadyManaged"
  | "runtime.status.cloudReadyOps"
  | "runtime.status.cloudOff"
  | "runtime.status.cloudOffHint"
  | "runtime.status.cloudMisconfigured"
  | "runtime.status.cloudRunnerUnavailable"
  | "runtime.status.cloudRunnerUnavailableHint"

interface BannerKeys {
  shape: Shape
  titleKey: StatusCopyKey
  hintKey?: StatusCopyKey
}

function describeStatus(
  s: {
    has_credential: boolean
    available: boolean
    sandbox_agent_count: number
    profile: string
    configured_by?: string
    credential_masked?: string | null
  },
): BannerKeys {
  // Managed: Parsar owns the credential; admins never see the
  // missing-cred path, regardless of sandbox-agent count.
  if (s.profile === "managed") {
    if (s.available) {
      return {
        shape: "ok",
        titleKey: "runtime.status.cloudReady",
        hintKey: "runtime.status.cloudReadyManaged",
      }
    }
    return { shape: "warn", titleKey: "runtime.status.cloudMisconfigured" }
  }

  // selfhost / oss: cred-aware branching.
  if (s.has_credential) {
    if (s.available) {
      return {
        shape: "ok",
        titleKey: "runtime.status.cloudReady",
        hintKey: s.profile === "selfhost"
          ? "runtime.status.cloudReadyOps"
          : "runtime.status.cloudReadyHint",
      }
    }
    return {
      shape: "warn",
      titleKey: "runtime.status.cloudRunnerUnavailable",
      hintKey: "runtime.status.cloudRunnerUnavailableHint",
    }
  }

  // No credential. If sandbox-agents already exist this is blocking
  // (pending wizard); otherwise the workspace just hasn't opted in.
  if (s.sandbox_agent_count > 0) {
    return { shape: "warn", titleKey: "runtime.status.cloudMisconfigured" }
  }
  return {
    shape: "info",
    titleKey: "runtime.status.cloudOff",
    hintKey: "runtime.status.cloudOffHint",
  }
}

function BannerView({
  shape,
  title,
  hint,
  action,
}: {
  shape: Shape
  title: string
  hint?: string
  action?: React.ReactNode
}) {
  const styles = SHAPE_STYLES[shape]
  const Icon = SHAPE_ICONS[shape]
  return (
    <div
      className={`mb-3 flex items-start gap-3 rounded-md border px-3 py-2.5 ${styles.container}`}
      role={shape === "warn" ? "alert" : "status"}
      data-testid={`runtime-status-banner-${shape}`}
    >
      <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${styles.icon}`} strokeWidth={1.75} />
      <div className="min-w-0 flex-1">
        <p className={`text-[13px] font-medium ${styles.title}`}>{title}</p>
        {hint && <p className={`mt-0.5 text-[13px] leading-relaxed ${styles.hint}`}>{hint}</p>}
      </div>
      {action}
    </div>
  )
}

const SHAPE_STYLES: Record<Shape, { container: string; icon: string; title: string; hint: string }> = {
  ok: {
    container: "border-emerald-200 bg-emerald-50/60",
    icon: "text-emerald-600",
    title: "text-emerald-900",
    hint: "text-emerald-700",
  },
  info: {
    container: "border-slate-200 bg-slate-50/80",
    icon: "text-slate-500",
    title: "text-slate-800",
    hint: "text-slate-600",
  },
  warn: {
    container: "border-amber-200 bg-amber-50/70",
    icon: "text-amber-600",
    title: "text-amber-900",
    hint: "text-amber-700",
  },
}

const SHAPE_ICONS: Record<Shape, typeof CheckCircle2> = {
  ok: CheckCircle2,
  info: Info,
  warn: AlertTriangle,
}
