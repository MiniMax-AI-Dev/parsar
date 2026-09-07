/**
 * Shared preview header for MCP / Skill / Plugin forms.
 */
import { AlertTriangle, Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { CapabilityTypeBadge } from "./CapabilityTypeBadge"
import { InlineNotice } from "./notices"
import type { CanonicalKind } from "./types"

interface Props {
  status: "idle" | "loading" | "error" | "ready"
  errorMessage?: string | null
  warnings?: string[]
  suggestedName?: string
  /**
   * Optional secondary line — currently used by Skill imports to show
   * the parsed `description` frontmatter alongside the name. MCP/Plugin
   * forms leave this undefined so they keep the simpler "just-the-name"
   * header.
   */
  description?: string
  /**
   * Capability kind, rendered as a badge next to the name. The kind is
   * already implicit from the active tab, but pinning it next to the
   * name is the cue that matches the ledger row layout.
   */
  kind?: CanonicalKind
}

export function ImportPreview({
  status,
  errorMessage,
  warnings = [],
  suggestedName,
  description,
  kind,
}: Props) {
  const { t } = useTranslation("admin")

  return (
    <div className="space-y-2">
      {status === "idle" && (
        <p className="text-sm text-fg-muted">
          {t("capabilities.import.preview.idle", "Paste content on the left to see the parsed result here")}
        </p>
      )}

      {status === "loading" && (
        <p className="inline-flex items-center gap-2 text-sm text-fg-muted">
          <Loader2 className="h-3.5 w-3.5 animate-spin" strokeWidth={1.5} aria-hidden="true" />
          {t("capabilities.import.preview.loading", "Parsing…")}
        </p>
      )}

      {status === "error" && errorMessage && <InlineNotice tone="error">{errorMessage}</InlineNotice>}

      {status === "ready" && suggestedName && (
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <code className="break-all font-mono text-sm font-medium text-fg">{suggestedName}</code>
            {kind && <CapabilityTypeBadge type={kind} />}
          </div>
          {description && <p className="mt-1 break-words text-sm text-fg">{description}</p>}
        </div>
      )}

      {warnings.length > 0 && (
        <div className="text-sm text-fg">
          <div className="flex items-center gap-2 font-medium">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-running" strokeWidth={1.5} aria-hidden="true" />
            {t("capabilities.import.preview.warnings", "Parse warnings")}
          </div>
          <ul className="m-0 mt-1 list-disc space-y-0.5 pl-6 text-xs">
            {warnings.map((w, i) => (
              <li key={i} className="break-all">{w}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
