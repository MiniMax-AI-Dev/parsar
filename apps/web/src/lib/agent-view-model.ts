import type { Agent, AgentDetail, Model } from "./api-types"

export type AgentEngine = "claude_code" | "codex" | "pi" | "opencode"

export type AgentExecutionMode = "local_device" | "sandbox" | "external"

export type AgentEngineLabelKey =
  | "agents.engine.claudeCode.title"
  | "agents.engine.codex.title"
  | "agents.engine.pi.title"
  | "agents.engine.opencode.title"

type AgentSource = Agent | AgentDetail | null | undefined
type UnknownRecord = Record<string, unknown>

function record(value: unknown): UnknownRecord {
  return value !== null && typeof value === "object" ? (value as UnknownRecord) : {}
}

function configOf(agent: AgentSource): UnknownRecord {
  return record(agent?.config)
}

function profileOf(agent: AgentSource): UnknownRecord {
  const config = configOf(agent)
  return {
    ...record((agent as AgentDetail | undefined)?.profile),
    ...record(config.profile),
  }
}

function stringValue(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim()
    if (typeof value === "number" && Number.isFinite(value)) return String(value)
  }
  return ""
}

function normalizeEngine(value: string): AgentEngine | null {
  switch (value.trim().toLowerCase().replaceAll("-", "_")) {
    case "claude_code":
    case "claude":
      return "claude_code"
    case "codex":
      return "codex"
    case "pi":
      return "pi"
    case "opencode":
    case "open_code":
      return "opencode"
    default:
      return null
  }
}

export function agentEngineOf(agent: AgentSource): AgentEngine {
  const config = configOf(agent)
  const profile = profileOf(agent)
  return (
    normalizeEngine(
      stringValue(
        config.agent_kind,
        config.engine,
        profile.agent_kind,
        profile.engine,
        record(agent).agent_kind,
        record(agent).engine,
      ),
    ) ?? "claude_code"
  )
}

export function agentEngineLabel(engine: AgentEngine): AgentEngineLabelKey {
  switch (engine) {
    case "claude_code":
      return "agents.engine.claudeCode.title"
    case "codex":
      return "agents.engine.codex.title"
    case "pi":
      return "agents.engine.pi.title"
    case "opencode":
      return "agents.engine.opencode.title"
  }
}

export function agentExecutionModeOf(agent: AgentSource): AgentExecutionMode {
  const config = configOf(agent)
  const profile = profileOf(agent)
  const connector = stringValue(agent?.connector_type).toLowerCase()
  if (connector === "http" || connector === "http-agent") return "external"

  const daemonMode = stringValue(
    config.daemon_mode,
    config.execution_mode,
    profile.daemon_mode,
    profile.execution_mode,
  )
    .toLowerCase()
    .replaceAll("-", "_")
  if (daemonMode === "sandbox") return "sandbox"
  if (daemonMode === "local" || daemonMode === "local_device") return "local_device"

  if (agent?.runtime === "local") return "local_device"
  if (agent?.runtime === "sandbox") return "sandbox"
  return connector === "agent_daemon" ? "sandbox" : "local_device"
}

export function agentWorkdirOf(agent: AgentSource): string {
  const config = configOf(agent)
  const profile = profileOf(agent)
  return stringValue(
    config.work_dir,
    config.workdir,
    config.working_directory,
    profile.work_dir,
    profile.workdir,
    profile.working_directory,
  )
}

export type AgentSandboxSize = "standard" | "xl"

export function agentSandboxSizeOf(agent: AgentSource): AgentSandboxSize {
  const config = configOf(agent)
  const profile = profileOf(agent)
  const value = stringValue(config.sandbox_size, profile.sandbox_size).toLowerCase()
  return value === "xl" ? "xl" : "standard"
}

export function agentDefaultModelIDOf(agent: AgentSource): string {
  const config = configOf(agent)
  const profile = profileOf(agent)
  return stringValue(
    config.default_model_id,
    config.model_id,
    profile.default_model_id,
    profile.model_id,
  )
}

export function defaultModelOf(agent: AgentSource, models: Model[], unavailableLabel: string): string {
  const id = agentDefaultModelIDOf(agent)
  if (!id) return "—"
  const found = models.find((model) => model.id === id)
  if (!found) return unavailableLabel
  return found.name || found.model_key || id
}
