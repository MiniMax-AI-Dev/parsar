import type { StatusKind } from "../../../components/ui/status-icon"
import type { RuntimeLiveness } from "../../../lib/api-runtimes"

/** Daemon liveness → the 14px status icon. */
export const LIVENESS_STATUS: Record<RuntimeLiveness, StatusKind> = {
  online: "completed",
  offline: "failed",
  error: "interrupted",
  pending_pairing: "queued",
}

export function formatAgentKindLabel(kind: string): string {
  switch (kind) {
    case "claude_code":
      return "Claude Code"
    case "opencode":
      return "OpenCode"
    case "codex":
      return "Codex"
    case "pi":
      return "PI Agent"
    default:
      return kind
  }
}
