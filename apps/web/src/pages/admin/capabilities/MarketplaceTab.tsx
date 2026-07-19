import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { ArrowLeft, ArrowRight, PackageCheck, Search } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "../../../components/ui/tabs"
import { useMarketplaceList, type MarketplaceCapability, marketplaceSourceName } from "../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../lib/workspace"
import { requiredCredentialsLabel } from "../capability-ui"
import type { Capability } from "../../../lib/api-types"

interface MarketplaceTabProps {
  itemID: string | null
  onSelectItem: (id: string | null) => void
  onInstall: (capability: MarketplaceCapability) => void
}

type TypeFilter = "all" | "mcp" | "skill" | "plugin" | "system_prompt"

export function MarketplaceTab({ itemID, onSelectItem, onInstall }: MarketplaceTabProps) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const marketplaceQ = useMarketplaceList(workspaceID)
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("all")
  const [query, setQuery] = useState("")
  const [hideInstalled, setHideInstalled] = useState(true)

  const items = useMemo(() => marketplaceQ.data ?? [], [marketplaceQ.data])
  const selected = items.find((item) => item.id === itemID) ?? null
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((item) => {
      if (typeFilter !== "all" && item.type !== typeFilter) return false
      // "Hide what's already in this workspace" — both rows you published
      // and rows you installed from elsewhere are available locally.
      if (hideInstalled && (item.installed || item.self_published)) return false
      if (!needle) return true
      return `${item.name} ${item.description ?? ""}`.toLowerCase().includes(needle)
    })
  }, [items, query, typeFilter, hideInstalled])

  if (selected) {
    return (
      <MarketplaceItemDetail
        capability={selected}
        language={i18n.language}
        onBack={() => onSelectItem(null)}
        onInstall={() => onInstall(selected)}
      />
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Tabs value={typeFilter} onValueChange={(value) => setTypeFilter(value as TypeFilter)}>
          <TabsList>
            <TabsTrigger value="all">{t("capabilities.marketplace.filters.all")}</TabsTrigger>
            <TabsTrigger value="mcp">MCP</TabsTrigger>
            <TabsTrigger value="skill">Skill</TabsTrigger>
            <TabsTrigger value="plugin">Plugin</TabsTrigger>
            <TabsTrigger value="system_prompt">System Prompt</TabsTrigger>
          </TabsList>
        </Tabs>
        <label className="inline-flex select-none items-center gap-1.5 text-sm text-fg-muted">
          <input
            type="checkbox"
            className="h-3.5 w-3.5 rounded border-line-strong text-fg focus:ring-slate-400"
            checked={hideInstalled}
            onChange={(event) => setHideInstalled(event.target.checked)}
          />
          {t("capabilities.marketplace.filters.hideInstalled")}
        </label>
        <div className="relative ml-auto w-full max-w-[280px]">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 h-3.5 w-3.5 text-fg-faint" />
          <Input
            className="pl-8"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("capabilities.marketplace.filters.search")}
          />
        </div>
      </div>

      {marketplaceQ.isLoading ? (
        <div className="grid gap-3 md:grid-cols-2">
          {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-32 w-full" />)}
        </div>
      ) : marketplaceQ.error ? (
        <ErrorState
          title={t("capabilities.marketplace.loadError.title")}
          description={marketplaceQ.error instanceof Error ? marketplaceQ.error.message : t("capabilities.marketplace.loadError.description")}
          onRetry={() => void marketplaceQ.refetch()}
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.marketplace.empty.title")}
          description={t("capabilities.marketplace.empty.description")}
        />
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {filtered.map((item) => (
            <MarketplaceCard
              key={item.id}
              capability={item}
              language={i18n.language}
              onOpen={() => onSelectItem(item.id)}
              onInstall={() => onInstall(item)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function MarketplaceCard({ capability, language, onOpen, onInstall }: {
  capability: MarketplaceCapability
  language: string
  onOpen: () => void
  onInstall: () => void
}) {
  const { t } = useTranslation("admin")
  const source = marketplaceSourceName(capability)
  const count = capability.installed_agent_count ?? capability.enabled_agent_count ?? capability.install_count ?? 0
  return (
    <div className="rounded-lg border border-line bg-surface p-4 transition hover:border-line-strong hover:shadow-sm">
      <button type="button" className="w-full text-left" onClick={onOpen}>
        <div className="flex items-start justify-between gap-3">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-base font-medium text-fg">{capability.name}</h3>
              <CapabilityTypeBadge type={capability.type} />
              {capability.self_published && <Badge variant="neutral">{t("capabilities.marketplace.card.selfPublished")}</Badge>}
              {!capability.self_published && capability.installed && <Badge variant="success">{t("capabilities.marketplace.card.installedBadge")}</Badge>}
            </div>
            {source && <p className="mt-1 text-sm text-fg-subtle">{t("capabilities.marketplace.card.source", { source })}</p>}
          </div>
          <ArrowRight className="mt-1 h-3.5 w-3.5 text-fg-faint" />
        </div>
        {capability.description && <p className="mt-3 line-clamp-2 text-sm leading-5 text-fg-muted">{capability.description}</p>}
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm text-fg-subtle">
          <span>{t("capabilities.marketplace.card.latest", { version: capability.latest_version ?? "—" })}</span>
          <span>·</span>
          <span>{t("capabilities.marketplace.card.added", { count })}</span>
          <span>·</span>
          <span>{t("capabilities.marketplace.card.credential", { kind: requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none")) })}</span>
        </div>
      </button>
      <div className="mt-4 flex justify-end">
        <Button size="sm" disabled={capability.self_published} onClick={onInstall}>
          {capability.self_published
            ? t("capabilities.marketplace.card.selfPublished")
            : capability.installed
              ? t("capabilities.marketplace.card.installed", { count })
              : t("capabilities.marketplace.card.install")}
        </Button>
      </div>
    </div>
  )
}

function MarketplaceItemDetail({ capability, language, onBack, onInstall }: {
  capability: MarketplaceCapability
  language: string
  onBack: () => void
  onInstall: () => void
}) {
  const { t } = useTranslation("admin")
  const source = marketplaceSourceName(capability)
  return (
    <div className="space-y-3">
      <Button variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeft className="h-3.5 w-3.5" />
        {t("capabilities.marketplace.detail.back")}
      </Button>
      <div className="rounded-lg border border-line bg-surface p-5">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-lg font-semibold text-fg">{capability.name}</h3>
          <CapabilityTypeBadge type={capability.type} />
        </div>
        {source && <p className="mt-2 text-sm text-fg-subtle">{t("capabilities.marketplace.card.source", { source })}</p>}
        {capability.description && <p className="mt-4 text-sm leading-5 text-fg-muted">{capability.description}</p>}
        <div className="mt-4 grid gap-3 md:grid-cols-3">
          <Detail label={t("capabilities.table.latestVersion")} value={capability.latest_version ? `v${capability.latest_version}` : t("capabilities.none")} mono />
          <Detail label={t("capabilities.table.credentials")} value={requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none"))} />
          <Detail label={t("capabilities.marketplace.detail.addedCount")} value={String(capability.install_count ?? capability.installed_workspace_count ?? 0)} />
        </div>
        <div className="mt-5 flex justify-end">
          <Button size="sm" disabled={capability.self_published} onClick={onInstall}>
            {capability.self_published ? t("capabilities.marketplace.card.selfPublished") : t("capabilities.marketplace.card.install")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function Detail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-md border border-line p-3">
      <p className="text-xs text-fg-subtle">{label}</p>
      <p className={`mt-1 text-sm text-fg ${mono ? "font-mono" : ""}`}>{value}</p>
    </div>
  )
}

function CapabilityTypeBadge({ type }: { type: Capability["type"] }) {
  if (type === "skill") return <Badge variant="primary">Skill</Badge>
  if (type === "plugin") return <Badge variant="success">Plugin</Badge>
  return <Badge variant="neutral">MCP</Badge>
}
