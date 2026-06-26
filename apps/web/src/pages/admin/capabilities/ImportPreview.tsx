/**
 * Shared preview header for MCP / Skill / Plugin forms.
 */
import { AlertTriangle, Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

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
   * Capability kind, rendered as a small badge next to the name. The
   * kind is already implicit from the active tab, but pinning it next
   * to the name is the cue that matches the marketplace card layout.
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
        <p className="rounded-md border border-dashed border-slate-200 bg-slate-50 px-3 py-2 text-[12px] text-slate-500">
          {t("capabilities.import.preview.idle", "在左侧粘贴内容后,这里会显示解析结果")}
        </p>
      )}

      {status === "loading" && (
        <p className="inline-flex items-center gap-2 rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-[12px] text-slate-600">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          {t("capabilities.import.preview.loading", "正在解析…")}
        </p>
      )}

      {status === "error" && errorMessage && (
        <div
          role="alert"
          className="break-all rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-800"
        >
          {errorMessage}
        </div>
      )}

      {status === "ready" && suggestedName && (
        <div className="min-w-0 rounded-md border border-slate-200 bg-white px-3 py-2.5">
          <div className="flex items-center gap-2">
            <code className="break-all font-mono text-[14px] font-semibold text-slate-900">
              {suggestedName}
            </code>
            {kind && <KindBadge kind={kind} />}
          </div>
          {description && (
            <p className="mt-1 break-words text-[12px] leading-relaxed text-slate-600">
              {description}
            </p>
          )}
        </div>
      )}

      {warnings.length > 0 && (
        <div className="space-y-1 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-[12px] text-amber-800">
          <div className="flex items-center gap-1.5 font-medium">
            <AlertTriangle className="h-3.5 w-3.5" />
            {t("capabilities.import.preview.warnings", "解析提示")}
          </div>
          <ul className="list-disc space-y-0.5 pl-5">
            {warnings.map((w, i) => (
              <li key={i} className="break-all">{w}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function KindBadge({ kind }: { kind: CanonicalKind }) {
  const label = kind.toUpperCase()
  return (
    <span className="rounded-full bg-slate-900 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-white">
      {label}
    </span>
  )
}
