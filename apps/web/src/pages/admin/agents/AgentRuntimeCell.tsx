import { useTranslation } from "react-i18next"

import { cn } from "../../../lib/utils"
import { agentExecutionPlacement } from "../../../lib/agent-runtime"
import type { Agent } from "../../../lib/api-types"

type LivenessTone = "online" | "offline" | "pending"

function runtimeLivenessTone(agent: Agent): LivenessTone | null {
  if (!agent.runtime_id) return null
  const liveness = (agent.runtime_liveness ?? "").toLowerCase()
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
}: {
  tone: LivenessTone
  kind?: string
  name: string
  mono?: boolean
  title?: string
}) {
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-sm text-fg" title={title}>
      <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", DOT[tone])} aria-hidden="true" />
      {kind && <span className="shrink-0 text-xs text-fg-muted">{kind} ·</span>}
      <span className={cn("min-w-0 truncate", mono && "font-mono text-xs")}>{name}</span>
    </span>
  )
}

export function AgentRuntimeCell({ agent }: { agent: Agent }) {
  const { t } = useTranslation("admin")
  const placement = agentExecutionPlacement(agent)

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
      return <RuntimeLine tone={tone} kind="Sandbox" name={fullId} mono title={[fullId, status].filter(Boolean).join(" · ")} />
    }
    return <RuntimeLine tone="offline" kind="Sandbox" name={t("agents.runtimeCell.pending")} />
  }

  const name = (agent.runtime_name ?? "").trim()
  const runtimeID = (agent.runtime_id ?? "").trim()
  if (placement === "local" && runtimeID && name) {
    const tone = runtimeLivenessTone(agent) ?? "offline"
    return <RuntimeLine tone={tone} kind="Local" name={name} title={[name, agent.runtime_liveness].filter(Boolean).join(" · ")} />
  }

  return <RuntimeLine tone="pending" name={t("agents.runtimeCell.unbound")} />
}
