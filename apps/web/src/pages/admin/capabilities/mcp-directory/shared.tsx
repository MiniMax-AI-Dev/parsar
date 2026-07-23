import { ExternalLink, Server, ShieldCheck } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"

export function ConnectorIcon({
  item,
  large = false,
}: {
  item: MCPDirectoryItem
  large?: boolean
}) {
  const size = large ? "h-14 w-14 rounded-xl" : "h-11 w-11 rounded-lg"
  return (
    <span
      className={`flex shrink-0 items-center justify-center overflow-hidden border border-line bg-surface-muted ${size}`}
    >
      {item.icon_url ? (
        <img
          src={item.icon_url}
          alt=""
          referrerPolicy="no-referrer"
          className="h-full w-full object-cover"
        />
      ) : (
        <Server className={large ? "h-6 w-6 text-fg-subtle" : "h-5 w-5 text-fg-subtle"} />
      )}
    </span>
  )
}

export function VerifiedBadge() {
  const { t } = useTranslation("admin")
  return (
    <Badge variant="primary">
      <ShieldCheck className="h-3 w-3" /> {t("capabilities.mcpDirectory.verified")}
    </Badge>
  )
}

export function Metadata({
  label,
  value,
  mono = false,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="rounded-lg border border-line p-3">
      <p className="text-xs text-fg-subtle">{label}</p>
      <p className={`mt-1 text-sm text-fg ${mono ? "font-mono" : ""}`}>{value}</p>
    </div>
  )
}

export function ExternalLinkRow({
  label,
  value,
  href,
}: {
  label: string
  value: string
  href?: string
}) {
  const safeHref = safeExternalURL(href)
  return (
    <div>
      <p className="text-xs text-fg-subtle">{label}</p>
      {safeHref ? (
        <a
          href={safeHref}
          target="_blank"
          rel="noreferrer"
          className="mt-1 inline-flex items-center gap-1 text-sm text-fg underline decoration-line-strong underline-offset-2"
        >
          <span>{value}</span>
          <ExternalLink className="h-3 w-3" />
        </a>
      ) : (
        <p className="mt-1 text-sm text-fg-faint">—</p>
      )}
    </div>
  )
}

function safeExternalURL(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const url = new URL(value)
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : undefined
  } catch {
    return undefined
  }
}
