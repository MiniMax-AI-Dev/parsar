import { useTranslation } from "react-i18next"
import * as Tooltip from "@radix-ui/react-tooltip"

import { Badge } from "../../../components/ui/badge"
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

function StatusDot({ tone, title }: { tone: LivenessTone; title?: string }) {
  const color = tone === "online"
    ? "bg-success"
    : tone === "pending"
      ? "bg-warning"
      : "bg-surface-muted"

  return (
    <span
      className={`inline-block h-1.5 w-1.5 rounded-full ${color}`}
      title={title}
      aria-hidden="true"
    />
  )
}

function TruncatedName({
  display,
  full,
  className,
  maxWidthClass = "max-w-[160px]",
}: {
  display: string
  full?: string
  className?: string
  maxWidthClass?: string
}) {
  const tip = full ?? display
  return (
    <Tooltip.Provider delayDuration={150}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>
          <span className={`${maxWidthClass} cursor-help truncate ${className ?? ""}`}>
            {display}
          </span>
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content
            side="top"
            className="z-50 max-w-sm break-all rounded-md border border-line bg-surface px-3 py-2 text-sm leading-relaxed text-fg-muted shadow-lg"
          >
            {tip}
            <Tooltip.Arrow className="fill-white" />
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
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

      return (
        <span className="inline-flex items-center gap-1.5 text-sm text-fg-muted">
          <span className="font-medium text-fg-subtle">Sandbox</span>
          <span className="text-fg-faint">·</span>
          <TruncatedName
            display={fullId}
            className="font-mono text-fg-muted"
            maxWidthClass="max-w-[260px]"
          />
          <StatusDot tone={tone} title={status || undefined} />
        </span>
      )
    }

    return (
      <span className="inline-flex items-center gap-1.5 text-sm text-fg-subtle">
        <span className="font-medium">Sandbox</span>
        <span className="text-fg-faint">·</span>
        <span>{t("agents.runtimeCell.pending")}</span>
        <StatusDot tone="offline" />
      </span>
    )
  }

  const name = (agent.runtime_name ?? "").trim()
  const runtimeID = (agent.runtime_id ?? "").trim()
  if (placement === "local" && runtimeID && name) {
    const tone = runtimeLivenessTone(agent) ?? "offline"
    return (
      <span className="inline-flex items-center gap-1.5 text-sm text-fg-muted">
        <span className="font-medium text-fg-subtle">Local</span>
        <span className="text-fg-faint">·</span>
        <TruncatedName display={name} />
        <StatusDot tone={tone} title={agent.runtime_liveness || undefined} />
      </span>
    )
  }

  return (
    <Badge variant="warning" dot className="font-normal">
      {t("agents.runtimeCell.unbound")}
    </Badge>
  )
}
