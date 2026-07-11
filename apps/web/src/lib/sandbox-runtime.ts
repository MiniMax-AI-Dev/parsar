import { isSandboxDaemonRuntime, type Runtime } from "./api-runtimes"

export function sandboxRuntimeAgentID(runtime: Runtime): string {
  const raw = runtime.config.agent_id
  return typeof raw === "string" ? raw.trim() : ""
}

export function isSandboxPairingExpired(runtime: Runtime, nowMs = Date.now()): boolean {
  if (runtime.liveness !== "pending_pairing" || !runtime.pairing_token_expires_at) return false
  const expiresAt = new Date(runtime.pairing_token_expires_at).getTime()
  return !Number.isNaN(expiresAt) && expiresAt <= nowMs
}

export function findSandboxRuntimeForAgent(runtimes: Runtime[], agentID: string): Runtime | null {
  const matches = runtimes
    .filter(isSandboxDaemonRuntime)
    .filter((runtime) => sandboxRuntimeAgentID(runtime) === agentID)
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  return matches[0] ?? null
}
