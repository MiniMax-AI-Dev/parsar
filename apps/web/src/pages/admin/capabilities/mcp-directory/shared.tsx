import { Server, ShieldCheck } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"

/** 18px tile for a connector row: the publisher icon, or a server glyph. */
export function ConnectorIcon({ item }: { item: MCPDirectoryItem }) {
  return (
    <span aria-hidden="true" className="app-tile inline-flex h-[18px] w-[18px] shrink-0 items-center justify-center overflow-hidden rounded">
      {item.icon_url ? (
        <img src={item.icon_url} alt="" referrerPolicy="no-referrer" className="h-full w-full object-cover" />
      ) : (
        <Server className="h-3 w-3 text-fg" strokeWidth={1.5} />
      )}
    </span>
  )
}

export function VerifiedBadge() {
  const { t } = useTranslation("admin")
  return (
    <span className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-muted">
      <ShieldCheck className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
      {t("capabilities.mcpDirectory.verified")}
    </span>
  )
}
