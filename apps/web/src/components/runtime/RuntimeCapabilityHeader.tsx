import { useTranslation } from "react-i18next"

import { Skeleton } from "../ui/skeleton"
import { StatusIcon, type StatusKind } from "../ui/status-icon"
import { useRuntimeStatus } from "../../lib/api-runtime"

interface RuntimeCapabilityHeaderProps {
  workspaceID: string | null
}

type SandboxBucket = "not-configured" | "healthy" | "misconfigured" | "unreachable"

/**
 * Two 32px hairline rows summarising local and cloud capability: a status
 * icon, the capability name in ink, the state sentence muted.
 */
export function RuntimeCapabilityHeader({ workspaceID }: RuntimeCapabilityHeaderProps) {
  const { t } = useTranslation("admin")
  const statusQ = useRuntimeStatus(workspaceID)

  if (statusQ.isLoading) {
    return (
      <div className="mb-4" data-testid="runtime-capability-header-loading">
        <div className="flex h-8 items-center border-b border-line"><Skeleton className="h-3 w-48" /></div>
        <div className="flex h-8 items-center border-b border-line"><Skeleton className="h-3 w-64" /></div>
      </div>
    )
  }

  const sandbox = classifySandbox(statusQ.data, !!statusQ.error)
  const sandboxCopy = SANDBOX_COPY[sandbox]

  return (
    <ul className="m-0 mb-4 list-none p-0" data-testid="runtime-capability-header">
      <CapabilityRow
        status="queued"
        title={t("runtime.capability.local.title")}
        body={t("runtime.capability.local.placeholder")}
        testId="runtime-capability-local"
      />
      <CapabilityRow
        status={sandboxCopy.status}
        title={t("runtime.capability.sandbox.title")}
        body={t(sandboxCopy.bodyKey, { count: statusQ.data?.sandbox_agent_count ?? 0 })}
        testId={`runtime-capability-sandbox-${sandbox}`}
      />
    </ul>
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

type SandboxBodyKey =
  | "runtime.capability.sandbox.healthy"
  | "runtime.capability.sandbox.notConfigured"
  | "runtime.capability.sandbox.misconfigured"
  | "runtime.capability.sandbox.unreachable"

const SANDBOX_COPY: Record<SandboxBucket, { status: StatusKind; bodyKey: SandboxBodyKey }> = {
  healthy: { status: "completed", bodyKey: "runtime.capability.sandbox.healthy" },
  "not-configured": { status: "queued", bodyKey: "runtime.capability.sandbox.notConfigured" },
  misconfigured: { status: "interrupted", bodyKey: "runtime.capability.sandbox.misconfigured" },
  unreachable: { status: "failed", bodyKey: "runtime.capability.sandbox.unreachable" },
}

function CapabilityRow({
  status,
  title,
  body,
  testId,
}: {
  status: StatusKind
  title: string
  body: string
  testId: string
}) {
  return (
    <li className="flex h-8 items-center gap-2 border-b border-line text-sm" data-testid={testId}>
      <StatusIcon status={status} />
      <span className="shrink-0 font-medium text-fg">{title}</span>
      <span className="min-w-0 truncate text-xs text-fg-muted">{body}</span>
    </li>
  )
}
