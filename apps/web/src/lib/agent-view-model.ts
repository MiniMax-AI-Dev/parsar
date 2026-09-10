import type { TFunction } from "i18next"

import type { Agent, AgentDetail, CapabilityType, Model } from "./api-types"

export type AgentEngine = "claude_code" | "codex" | "pi" | "opencode"

export type CodexCollaborationMode = "default" | "plan"

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

export function agentEngineSupportsCapability(engine: AgentEngine, capabilityType: CapabilityType): boolean {
  if (capabilityType === "knowledge") return true
  switch (engine) {
    case "claude_code":
      return true
    case "codex":
      return capabilityType === "mcp" || capabilityType === "system_prompt"
    case "opencode":
      return capabilityType === "skill" || capabilityType === "mcp" || capabilityType === "system_prompt"
    case "pi":
      return capabilityType === "skill" || capabilityType === "system_prompt"
  }
}

export function agentEnginesSupportingCapability(capabilityType: CapabilityType): AgentEngine[] {
  return (["claude_code", "codex", "pi", "opencode"] as const).filter((engine) =>
    agentEngineSupportsCapability(engine, capabilityType),
  )
}

export function agentCodexModeOf(agent: AgentSource): CodexCollaborationMode {
  if (agentEngineOf(agent) !== "codex") return "default"
  const config = configOf(agent)
  const profile = profileOf(agent)
  return stringValue(config.mode, profile.mode).toLowerCase() === "plan" ? "plan" : "default"
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

/**
 * The free-text search behind the Agent list. It lives here rather than in the
 * table because the filter menu counts the same set: two copies of this
 * predicate would let the counts disagree with the rows under them.
 */
export function searchAgents(
  agents: Agent[],
  keyword: string,
  models: Model[],
  t: TFunction<"admin">,
): Agent[] {
  const query = keyword.trim().toLowerCase()
  if (!query) return agents
  const unavailable = t("agents.modelUnavailable")
  return agents.filter((agent) =>
    agent.name.toLowerCase().includes(query)
    || agent.description.toLowerCase().includes(query)
    || agent.slug.toLowerCase().includes(query)
    || t(agentEngineLabel(agentEngineOf(agent))).toLowerCase().includes(query)
    || defaultModelOf(agent, models, unavailable).toLowerCase().includes(query)
    || agentConnectorLabel(agent.connector_type).toLowerCase().includes(query),
  )
}

/** The connector types an Agent can carry, in the order the filter lists them. */
export const AGENT_CONNECTOR_TYPES = ["agent_daemon", "http", "a2a"] as const

/** Normalises the legacy `http-agent` spelling onto the value space above. */
export function agentConnectorKey(connectorType: string): string {
  return connectorType === "http-agent" ? "http" : connectorType
}

export function agentConnectorLabel(connectorType: string): string {
  switch (agentConnectorKey(connectorType)) {
    case "agent_daemon": return "Agent Daemon"
    case "http": return "HTTP Agent"
    case "a2a": return "A2A"
    default: return connectorType
  }
}
