import { useTranslation } from "react-i18next"
import { CheckCircle2, AlertTriangle, Cpu, KeyRound, Cloud } from "lucide-react"

import { Skeleton } from "../ui/skeleton"
import { useRuntimeStatus } from "../../lib/api-runtime"

interface RuntimeCapabilityHeaderProps {
  workspaceID: string | null
}

type SandboxBucket = "not-configured" | "healthy" | "misconfigured" | "unreachable"

export function RuntimeCapabilityHeader({ workspaceID }: RuntimeCapabilityHeaderProps) {
  const { t } = useTranslation("admin")
  const statusQ = useRuntimeStatus(workspaceID)

  if (statusQ.isLoading) {
    return (
      <div className="mb-4 flex flex-wrap gap-3" data-testid="runtime-capability-header-loading">
        <Skeleton className="h-12 w-64 rounded-md" />
        <Skeleton className="h-12 w-72 rounded-md" />
      </div>
    )
  }

  const sandbox = classifySandbox(statusQ.data, !!statusQ.error)

  return (
    <div className="mb-4 flex flex-wrap gap-3" data-testid="runtime-capability-header">
      <Chip
        Icon={Cpu}
        title={t("runtime.capability.local.title")}
        body={t("runtime.capability.local.placeholder")}
        tone="info"
        testId="runtime-capability-local"
      />
      <SandboxChip
        bucket={sandbox}
        agentCount={statusQ.data?.sandbox_agent_count ?? 0}
      />
    </div>
  )
}

function classifySandbox(
  status: ReturnType<typeof useRuntimeStatus>["data"],
  unreachable: boolean,
): SandboxBucket {
  if (unreachable || !status) return "unreachable"
  if (status.profile === "managed") return status.available ? "healthy" : "misconfigured"
  if (!status.has_credential) return "not-configured"
  if (status.available) return "healthy"
  return "misconfigured"
}

function SandboxChip({ bucket, agentCount }: { bucket: SandboxBucket; agentCount: number }) {
  const { t } = useTranslation("admin")
  switch (bucket) {
    case "healthy":
      return (
        <Chip
          Icon={CheckCircle2}
          title={t("runtime.capability.sandbox.title")}
          body={t("runtime.capability.sandbox.healthy", { count: agentCount })}
          tone="ok"
          testId="runtime-capability-sandbox-healthy"
        />
      )
    case "not-configured":
      return (
        <Chip
          Icon={KeyRound}
          title={t("runtime.capability.sandbox.title")}
          body={t("runtime.capability.sandbox.notConfigured")}
          tone="info"
          testId="runtime-capability-sandbox-not-configured"
        />
      )
    case "misconfigured":
      return (
        <Chip
          Icon={AlertTriangle}
          title={t("runtime.capability.sandbox.title")}
          body={t("runtime.capability.sandbox.misconfigured")}
          tone="warn"
          testId="runtime-capability-sandbox-misconfigured"
        />
      )
    case "unreachable":
      return (
        <Chip
          Icon={Cloud}
          title={t("runtime.capability.sandbox.title")}
          body={t("runtime.capability.sandbox.unreachable")}
          tone="warn"
          testId="runtime-capability-sandbox-unreachable"
        />
      )
  }
}

type ChipTone = "info" | "ok" | "warn"

function Chip({
  Icon,
  title,
  body,
  tone,
  testId,
}: {
  Icon: typeof CheckCircle2
  title: string
  body: string
  tone: ChipTone
  testId: string
}) {
  const styles = TONE_STYLES[tone]
  return (
    <div
      className={`flex items-start gap-2 rounded-md border px-3 py-2 ${styles.container}`}
      data-testid={testId}
    >
      <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${styles.icon}`} strokeWidth={1.75} />
      <div className="min-w-0">
        <p className={`text-[12px] font-medium leading-tight ${styles.title}`}>{title}</p>
        <p className={`mt-0.5 text-[11.5px] leading-snug ${styles.body}`}>{body}</p>
      </div>
    </div>
  )
}

const TONE_STYLES: Record<ChipTone, { container: string; icon: string; title: string; body: string }> = {
  info: {
    container: "border-slate-200 bg-slate-50/80",
    icon: "text-slate-500",
    title: "text-slate-800",
    body: "text-slate-600",
  },
  ok: {
    container: "border-emerald-200 bg-emerald-50/70",
    icon: "text-emerald-600",
    title: "text-emerald-900",
    body: "text-emerald-700",
  },
  warn: {
    container: "border-amber-200 bg-amber-50/70",
    icon: "text-amber-600",
    title: "text-amber-900",
    body: "text-amber-700",
  },
}
