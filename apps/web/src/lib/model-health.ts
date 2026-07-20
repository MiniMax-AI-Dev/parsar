import type { Model, ModelHealth, ModelHealthStatus } from "./api-types"

const HEALTH_STATUSES: ModelHealthStatus[] = ["healthy", "failed", "unsupported", "untested"]

function isHealthStatus(value: unknown): value is ModelHealthStatus {
  return typeof value === "string" && HEALTH_STATUSES.includes(value as ModelHealthStatus)
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value : undefined
}

export function modelHealth(model: Model): ModelHealth {
  const raw = model.config?.health
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { status: "untested" }
  }
  const data = raw as Record<string, unknown>
  return {
    status: isHealthStatus(data.status) ? data.status : "untested",
    checked_at: stringValue(data.checked_at),
    endpoint_type: stringValue(data.endpoint_type),
    latency_ms: numberValue(data.latency_ms),
    http_status: numberValue(data.http_status),
    error: stringValue(data.error),
    sample: stringValue(data.sample),
  }
}

export function isModelHealthy(model: Model): boolean {
  return modelHealth(model).status === "healthy"
}
