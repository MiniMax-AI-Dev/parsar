import type { ReactNode } from "react"
import { ArrowUpRight, Download, Link2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { DetailRail, RailSection } from "../../../../components/ui/detail-rail"
import { ErrorState } from "../../../../components/ui/error-state"
import { PropertyList, Property } from "../../../../components/ui/property-list"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { ExternalLinkValue, safeExternalURL } from "../notices"
import { ConnectorIcon, VerifiedBadge } from "./shared"

/**
 * A directory entry read in the rail beside the list: identity in the header,
 * the one thing you can do with it in the footer. Nothing here is a workplace,
 * so it never takes over the page.
 */
export function DirectoryDetail({
  item,
  loading,
  error,
  canImport,
  open,
  onClosed,
  onClose,
  onRetry,
  onImport,
  onConnect,
  onViewCapability,
}: {
  item: MCPDirectoryItem | null
  loading: boolean
  error: unknown
  canImport: boolean
  open: boolean
  onClosed: () => void
  onClose: () => void
  onRetry: () => void
  onImport: () => void
  onConnect: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")

  const header: ReactNode = item ? (
    <>
      <ConnectorIcon item={item} />
      <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{item.name}</span>
      {item.verified ? <VerifiedBadge /> : null}
      {item.installed ? (
        <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.actions.installed")}</Badge>
      ) : item.connected ? (
        <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.oauth.connected")}</Badge>
      ) : null}
    </>
  ) : (
    <Skeleton className="h-3 w-40" />
  )

  const footer = item ? (
    item.authentication === "oauth2" && !item.connected ? (
      <Button onClick={onConnect}>
        <Link2 strokeWidth={1.5} aria-hidden="true" />
        {t("capabilities.mcpDirectory.oauth.connect")}
      </Button>
    ) : item.installed && item.installed_capability_id ? (
      <Button variant="outline" onClick={() => onViewCapability(item.installed_capability_id!)}>
        {t("capabilities.mcpDirectory.actions.viewCapability")}
        <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
      </Button>
    ) : (
      <Button disabled={!canImport} title={!canImport ? t("capabilities.permission.adminOnly") : undefined} onClick={onImport}>
        <Download strokeWidth={1.5} aria-hidden="true" />
        {t("capabilities.mcpDirectory.actions.import")}
      </Button>
    )
  ) : undefined

  return (
    <DetailRail
      open={open}
      onClosed={onClosed}
      onClose={onClose}
      closeLabel={t("capabilities.mcpDirectory.actions.back")}
      aria-label={item?.name ?? t("capabilities.mcpDirectory.title")}
      data-testid="mcp-directory-detail"
      header={header}
      footer={footer}
    >
      {error ? (
        <ErrorState
          title={t("capabilities.mcpDirectory.detail.loadError")}
          description={error instanceof Error ? error.message : ""}
          onRetry={onRetry}
        />
      ) : !item ? (
        <div className="space-y-3">
          {Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-3 w-full" />)}
        </div>
      ) : (
        <DirectoryDetailBody item={item} loading={loading} />
      )}
    </DetailRail>
  )
}

function DirectoryDetailBody({ item, loading }: { item: MCPDirectoryItem; loading: boolean }) {
  const { t } = useTranslation("admin")
  const auth = item.authentication === "oauth2"
    ? item.connected
      ? t("capabilities.mcpDirectory.oauth.connected")
      : t("capabilities.mcpDirectory.oauth.required")
    : t("capabilities.mcpDirectory.detail.noAuthentication")
  const publisherURL = safeExternalURL(item.publisher.url)
  const homepageURL = safeExternalURL(item.homepage_url)
  const repositoryURL = safeExternalURL(item.repository_url)

  return (
    <>
      {item.description && <p className="mb-4 text-sm text-fg">{item.description}</p>}
      <RailSection title={t("capabilities.detail.basic.title")}>
        <PropertyList>
          <Property label={t("capabilities.mcpDirectory.detail.publisher")}>
            {publisherURL ? <ExternalLinkValue href={publisherURL}>{item.publisher.name}</ExternalLinkValue> : item.publisher.name}
          </Property>
          <Property label={t("capabilities.mcpDirectory.detail.version")} mono>{item.version || "—"}</Property>
          <Property label={t("capabilities.mcpDirectory.detail.transport")} mono>{item.transport}</Property>
          <Property label={t("capabilities.mcpDirectory.detail.authentication")}>{auth}</Property>
          <Property label={t("capabilities.mcpDirectory.detail.endpoint")} mono className="h-auto min-h-7 whitespace-normal break-all py-1">{item.url || "—"}</Property>
          <Property label={t("capabilities.mcpDirectory.filters.category")}>{item.categories.join(" · ") || "—"}</Property>
          <Property label={t("capabilities.mcpDirectory.detail.homepage")}>
            {homepageURL ? <ExternalLinkValue href={homepageURL}>{t("capabilities.mcpDirectory.detail.openLink")}</ExternalLinkValue> : "—"}
          </Property>
          <Property label={t("capabilities.mcpDirectory.detail.repository")}>
            {repositoryURL ? <ExternalLinkValue href={repositoryURL}>{t("capabilities.mcpDirectory.detail.openLink")}</ExternalLinkValue> : "—"}
          </Property>
        </PropertyList>
      </RailSection>
      {loading && <Skeleton className="mt-4 h-3 w-24" />}
    </>
  )
}
