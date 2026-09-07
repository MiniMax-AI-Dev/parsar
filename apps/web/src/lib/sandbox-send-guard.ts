import type { TFunction } from "i18next"

import { agentNeedsSandbox } from "./agent-runtime"
import type { Agent } from "./api-types"
import type { SandboxBinding } from "./api-sandbox"

/** Why the composer is closed, when it is. */
export interface SandboxSendGuard {
  blocked: boolean
  message: string
}

/**
 * A sandbox agent with no live binding cannot serve a prompt, so the composer
 * says so instead of accepting a send that will fail unexplained. Shared with
 * the bare shell — the guard is a property of the agent, not of the console.
 */
export function sandboxSendGuard(
  t: TFunction<"admin">,
  agent: Agent | undefined,
  binding: SandboxBinding | null | undefined,
  loading: boolean,
  error: unknown,
): SandboxSendGuard | undefined {
  if (!agentNeedsSandbox(agent)) return undefined
  if (loading) {
    return { blocked: true, message: t("conversations.sandboxGuard.checking") }
  }
  if (error) {
    const detail =
      error instanceof Error ? error.message : t("conversations.sandboxGuard.errorFallback")
    return { blocked: true, message: t("conversations.sandboxGuard.error", { error: detail }) }
  }
  if (!binding) {
    return { blocked: true, message: t("conversations.sandboxGuard.missing") }
  }
  if (binding.status_kind !== "live") {
    return {
      blocked: true,
      message: t("conversations.sandboxGuard.notLive", { status: binding.status }),
    }
  }
  return { blocked: false, message: "" }
}
