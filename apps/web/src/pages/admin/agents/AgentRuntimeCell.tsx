import { useTranslation } from "react-i18next"

import { cn } from "../../../lib/utils"
import { agentExecutionPlacement } from "../../../lib/agent-runtime"
import type { Agent } from "../../../lib/api-types"
import { useAgentRuntimeBinding } from "../../../lib/use-agent-runtime-binding"

type LivenessTone = "online" | "offline" | "pending"

function runtimeLivenessTone(value?: string): LivenessTone | null {
  const liveness = (value ?? "").toLowerCase()
  if (!liveness) return null
  if (liveness === "online" || liveness === "live") return "online"
  if (liveness === "pending_pairing" || liveness === "pending") return "pending"
  return "offline"
}

/* State lives in the 6px dot (the Badge dot idiom); the words stay in ink. */
const DOT: Record<LivenessTone, string> = {
  online: "bg-status-completed",
  pending: "bg-status-running",
  offline: "bg-status-queued",
}

function RuntimeLine({
  tone,
  kind,
  name,
  mono,
  title,
  className,
}: {
  tone: LivenessTone | null
  kind?: string
  name: string
  mono?: boolean
  title?: string
  className?: string
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-1.5 text-sm text-fg", className)} title={title}>
      {tone && <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", DOT[tone])} aria-hidden="true" />}
      {kind && <span className="shrink-0 text-xs text-fg-muted">{kind} ·</span>}
      <span className={cn("min-w-0 truncate", mono && "font-mono text-xs")}>{name}</span>
    </span>
  )
}

export function AgentRuntimeCell({ agent, className }: { agent: Agent; className?: string }) {
  const { t } = useTranslation("admin")
  const placement = agentExecutionPlacement(agent)
  const binding = useAgentRuntimeBinding(agent)

  if (placement === "sandbox") {
    const fullId = (agent.sandbox_external_id ?? "").trim()
    if (fullId) {
      const status = (agent.sandbox_status ?? "").toLowerCase()
      const tone: LivenessTone =
        status === "running"
          ? "online"
          : status === "spawning" || status === "renewing"
            ? "pending"
            : "offline"
      return <RuntimeLine className={className} tone={tone} kind="Sandbox" name={fullId} mono title={[fullId, status].filter(Boolean).join(" · ")} />
    }
    return <RuntimeLine className={className} tone="offline" kind="Sandbox" name={t("agents.runtimeCell.pending")} />
  }

  if (placement === "local" && binding.id) {
    return <RuntimeLine className={className} tone={runtimeLivenessTone(binding.liveness)} kind="Local" name={binding.name} title={[binding.name, binding.liveness].filter(Boolean).join(" · ")} />
  }

  return <RuntimeLine className={className} tone="pending" name={t("agents.runtimeCell.unbound")} />
}
