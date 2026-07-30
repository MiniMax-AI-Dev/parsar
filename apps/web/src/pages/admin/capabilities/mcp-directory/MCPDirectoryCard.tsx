import { ArrowRight, Check, Server, Trash2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { marketplaceSourceName, type MCPDirectoryItem, type MarketplaceCapability } from "../../../../lib/api-marketplace"
import { ConnectorIcon, VerifiedBadge } from "./shared"

export function DirectoryCard({ item, canImport, onOpen, onImport, onViewCapability }: {
  item: MCPDirectoryItem
  canImport: boolean
  onOpen: () => void
  onImport: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <article className="flex min-h-52 flex-col rounded-xl border border-line bg-surface p-4 transition hover:border-line-strong hover:shadow-sm" data-testid="mcp-directory-card" data-catalog-id={item.id}>
      <button type="button" className="flex flex-1 flex-col text-left" onClick={onOpen}>
        <div className="flex items-start gap-3">
          <ConnectorIcon item={item} />
          <div className="min-w-0 flex-1">
            <div className="flex items-start justify-between gap-2">
              <h3 className="truncate text-base font-semibold text-fg">{item.name}</h3>
              <ArrowRight className="mt-1 h-3.5 w-3.5 shrink-0 text-fg-faint" />
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-fg-subtle">
              <span className="truncate">{item.publisher.name}</span>
              {item.verified ? <VerifiedBadge /> : null}
            </div>
          </div>
        </div>
        <p className="mt-3 line-clamp-3 text-sm leading-5 text-fg-muted">{item.description}</p>
        <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-4">
          {item.categories.slice(0, 2).map((category) => <Badge key={category} variant="neutral">{category}</Badge>)}
        </div>
      </button>
      <div className="mt-3 border-t border-line pt-3">
        {item.installed && item.installed_capability_id ? (
          <Button className="w-full" variant="outline" size="sm" onClick={() => onViewCapability(item.installed_capability_id!)}>
            <Check className="h-3.5 w-3.5" /> {t("capabilities.mcpDirectory.actions.installed")}
          </Button>
        ) : (
          <Button className="w-full" size="sm" disabled={!canImport} title={!canImport ? t("capabilities.permission.adminOnly") : undefined} onClick={onImport}>
            {canImport ? t("capabilities.mcpDirectory.actions.import") : t("capabilities.permission.adminOnly")}
          </Button>
        )}
      </div>
    </article>
  )
}

export function MarketplaceMCPCard({ capability, canManage, onOpen, onInstall, onDelete, onViewCapability }: {
  capability: MarketplaceCapability
  canManage: boolean
  onOpen: () => void
  onInstall: () => void
  onDelete: () => void
  onViewCapability: () => void
}) {
  const { t } = useTranslation("admin")
  const source = marketplaceSourceName(capability)
  const count = capability.installed_agent_count ?? capability.enabled_agent_count ?? capability.install_count ?? 0
  return (
    <article className="flex min-h-52 flex-col rounded-xl border border-line bg-surface p-4 transition hover:border-line-strong hover:shadow-sm" data-testid="marketplace-mcp-card">
      <button type="button" className="flex flex-1 flex-col text-left" onClick={onOpen}>
        <div className="flex items-start gap-3">
          <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-line bg-surface-muted text-fg-subtle"><Server className="h-4 w-4" /></span>
          <div className="min-w-0 flex-1">
            <div className="flex items-start justify-between gap-2">
              <h3 className="truncate text-base font-semibold text-fg">{capability.name}</h3>
              <ArrowRight className="mt-1 h-3.5 w-3.5 shrink-0 text-fg-faint" />
            </div>
            {source ? <p className="mt-1 truncate text-xs text-fg-subtle">{source}</p> : null}
          </div>
        </div>
        {capability.description ? <p className="mt-3 line-clamp-3 text-sm leading-5 text-fg-muted">{capability.description}</p> : null}
        <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-4">
          <Badge variant="neutral">MCP</Badge>
          {capability.self_published ? <Badge variant="neutral">{t("capabilities.marketplace.card.selfPublished")}</Badge> : null}
          {!capability.self_published && capability.installed ? <Badge variant="success">{t("capabilities.marketplace.card.installedBadge")}</Badge> : null}
          <Badge variant="neutral">v{capability.latest_version ?? "—"}</Badge>
        </div>
      </button>
      <div className="mt-3 border-t border-line pt-3">
        {capability.self_published ? (
          <div className="flex gap-2">
            <Button className="min-w-0 flex-1" variant="outline" size="sm" onClick={onViewCapability}>{t("capabilities.mcpDirectory.actions.viewCapability")}</Button>
            {canManage ? <Button variant="destructive" size="sm" onClick={onDelete}><Trash2 className="h-3.5 w-3.5" />{t("capabilities.rowActions.delete")}</Button> : null}
          </div>
        ) : (
          <Button className="w-full" size="sm" onClick={onInstall}>
            {capability.installed
              ? t("capabilities.marketplace.card.installed", { count })
              : t("capabilities.marketplace.card.install")}
          </Button>
        )}
      </div>
    </article>
  )
}
