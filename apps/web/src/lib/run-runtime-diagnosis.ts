import type { AgentRunDetail } from "./api-types"

export type AdminText = (key: string, options?: Record<string, unknown>) => string
export type DiagnosisTone = "success" | "warning" | "error" | "neutral"

interface RuntimeDiagnosis {
  tone: DiagnosisTone
  health: string
  heartbeatAge: string
  action: string
}

function fmtAge(value: string | undefined, t: AdminText): string {
  if (!value) return t("runs.detail.diagnostics.age.unknown")
  const ms = Date.parse(value)
  if (Number.isNaN(ms)) return t("runs.detail.diagnostics.age.unknown")
  const seconds = Math.max(0, Math.round((Date.now() - ms) / 1000))
  if (seconds < 60) return t("runs.detail.diagnostics.age.seconds", { count: seconds })
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return t("runs.detail.diagnostics.age.minutes", { count: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 48) return t("runs.detail.diagnostics.age.hours", { count: hours })
  return t("runs.detail.diagnostics.age.days", { count: Math.round(hours / 24) })
}

export function buildRuntimeDiagnosis(run: AgentRunDetail, t: AdminText): RuntimeDiagnosis {
  const runtime = run.runtime
  if (!runtime) {
    const needsSnapshot = run.connector_type === "agent_daemon" && ["queued", "running", "failed"].includes(run.status)
    return {
      tone: needsSnapshot ? "warning" : "neutral",
      health: t("runs.detail.diagnostics.runtimeHealth.noSnapshot"),
      heartbeatAge: t("runs.detail.diagnostics.age.unknown"),
      action: t("runs.detail.diagnostics.runtimeActions.noSnapshot"),
    }
  }
  const live = (runtime.liveness ?? "").toLowerCase()
  const heartbeatMs = runtime.last_heartbeat_at ? Date.parse(runtime.last_heartbeat_at) : NaN
  const staleHeartbeat = run.status === "running" && Number.isFinite(heartbeatMs) && Date.now() - heartbeatMs > 120_000
  if (/offline|unhealthy|error|degraded/.test(live)) {
    return { tone: "error", health: t("runs.detail.diagnostics.runtimeHealth.offline"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.offline") }
  }
  if (staleHeartbeat) {
    return { tone: "warning", health: t("runs.detail.diagnostics.runtimeHealth.stale"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.stale") }
  }
  if (live !== "online") {
    return { tone: "neutral", health: t("runs.detail.diagnostics.runtimeHealth.unknown"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.unknown") }
  }
  return { tone: "success", health: t("runs.detail.diagnostics.runtimeHealth.ready"), heartbeatAge: fmtAge(runtime.last_heartbeat_at, t), action: t("runs.detail.diagnostics.runtimeActions.ready") }
}
