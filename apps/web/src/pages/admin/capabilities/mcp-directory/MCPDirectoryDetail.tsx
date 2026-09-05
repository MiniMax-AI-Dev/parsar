import type { ReactNode } from "react"
import { ArrowUpRight, Download, Link2, Server } from "lucide-react"
import { useTranslation } from "react-i18next"

import { PageHeader } from "../../../../components/layout/PageHeader"
import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { RailSection } from "../../../../components/ui/detail-rail"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { PropertyList, Property } from "../../../../components/ui/property-list"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { BackLink, ExternalLinkValue, safeExternalURL } from "../notices"
import { VerifiedBadge } from "./shared"

export function DirectoryDetail({
  item,
  loading,
  error,
  canImport,
  notices,
  onBack,
  onRetry,
  onImport,
  onConnect,
  onViewCapability,
}: {
  item: MCPDirectoryItem | null
  loading: boolean
  error: unknown
  canImport: boolean
  notices?: ReactNode
  onBack: () => void
  onRetry: () => void
  onImport: () => void
  onConnect: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  const backLabel = t("capabilities.mcpDirectory.actions.back")
  const header = (title: ReactNode, action?: ReactNode) => (
    <PageHeader className="static mx-0 mb-0" backLink={<BackLink label={backLabel} onClick={onBack} />} title={title} action={action} />
  )

  if (loading && !item) {
    return (
      <>
        {header(<Skeleton className="h-4 w-40" />)}
        <div className="space-y-3 px-6 pt-4">{Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-3 w-full max-w-lg" />)}</div>
      </>
    )
  }
  if (error) {
    return (
      <>
        {header(t("capabilities.mcpDirectory.detail.loadError"))}
        <div className="px-6 pt-4">
          <ErrorState title={t("capabilities.mcpDirectory.detail.loadError")} description={error instanceof Error ? error.message : ""} onRetry={onRetry} />
        </div>
      </>
    )
  }
  if (!item) {
    return (
      <>
        {header(t("capabilities.mcpDirectory.detail.notFound"))}
        <EmptyState icon={Server} title={t("capabilities.mcpDirectory.detail.notFound")} />
      </>
    )
  }

  const auth = item.authentication === "oauth2"
    ? item.connected
      ? t("capabilities.mcpDirectory.oauth.connected")
      : t("capabilities.mcpDirectory.oauth.required")
    : t("capabilities.mcpDirectory.detail.noAuthentication")
  const publisherURL = safeExternalURL(item.publisher.url)
  const homepageURL = safeExternalURL(item.homepage_url)
  const repositoryURL = safeExternalURL(item.repository_url)

  const action = item.authentication === "oauth2" && !item.connected ? (
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

  return (
    <>
      {header(
        <span className="inline-flex min-w-0 items-center gap-2">
          <span className="truncate">{item.name}</span>
          {item.verified ? <VerifiedBadge /> : null}
          {item.installed ? (
            <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.actions.installed")}</Badge>
          ) : item.connected ? (
            <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.oauth.connected")}</Badge>
          ) : null}
        </span>,
        action,
      )}
      {notices}
      <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-10 pt-4" data-testid="mcp-directory-detail">
        {item.description && <p className="mb-4 max-w-3xl text-sm text-fg">{item.description}</p>}
        <RailSection title={t("capabilities.detail.basic.title")}>
          <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
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
      </div>
    </>
  )
}
