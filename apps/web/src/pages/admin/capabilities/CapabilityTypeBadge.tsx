import { useTranslation } from "react-i18next"
import { cn } from "../../../lib/utils"
import type { CapabilityType } from "../../../lib/api-types"

const TYPE_LABEL: Record<CapabilityType, string> = {
  knowledge: "Knowledge base",
  mcp: "MCP",
  skill: "Skill",
  plugin: "Plugin",
  bundle: "Plugin Bundle",
  system_prompt: "System Prompt",
}

/** The capability type as 12px muted metadata after the name; no chip, no colour. */
export function CapabilityTypeBadge({ type, className }: { type: CapabilityType | string; className?: string }) {
  const { t } = useTranslation("admin")
  const label = type === "knowledge" ? t("capabilities.knowledge.title") : TYPE_LABEL[type as CapabilityType] ?? type
  return <span className={cn("shrink-0 text-xs text-fg-muted", className)}>{label}</span>
}
