import { ArrowLeft, Server, ShieldCheck } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { ConnectorIcon, ExternalLinkRow, Metadata, VerifiedBadge } from "./shared"

export function DirectoryDetail({
  item,
  loading,
  error,
  canImport,
  onBack,
  onRetry,
  onImport,
  onViewCapability,
}: {
  item: MCPDirectoryItem | null
  loading: boolean
  error: unknown
  canImport: boolean
  onBack: () => void
  onRetry: () => void
  onImport: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  if (loading && !item)
    return (
      <div className="space-y-3">
        <Skeleton className="h-9 w-40" />
        <Skeleton className="h-[440px] w-full" />
      </div>
    )
  if (error)
    return (
      <ErrorState
        title={t("capabilities.mcpDirectory.detail.loadError")}
        description={error instanceof Error ? error.message : ""}
        onRetry={onRetry}
      />
    )
  if (!item)
    return (
      <EmptyState
        icon={Server}
        title={t("capabilities.mcpDirectory.detail.notFound")}
        action={
          <Button variant="outline" size="sm" onClick={onBack}>
            {t("capabilities.mcpDirectory.actions.back")}
          </Button>
        }
      />
    )
  return (
    <div className="space-y-3" data-testid="mcp-directory-detail">
      <Button variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeft className="h-3.5 w-3.5" /> {t("capabilities.mcpDirectory.actions.back")}
      </Button>
      <article className="rounded-xl border border-line bg-surface p-5">
        <div className="flex flex-wrap items-start gap-4">
          <ConnectorIcon item={item} large />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-xl font-semibold text-fg">{item.name}</h2>
              {item.verified ? <VerifiedBadge /> : null}
              {item.installed ? (
                <Badge variant="success">{t("capabilities.mcpDirectory.actions.installed")}</Badge>
              ) : null}
            </div>
            <p className="mt-1 text-sm text-fg-subtle">{item.publisher.name}</p>
            <p className="mt-4 max-w-3xl text-sm leading-6 text-fg-muted">{item.description}</p>
          </div>
        </div>
        <div className="mt-5 grid gap-3 sm:grid-cols-3">
          <Metadata
            label={t("capabilities.mcpDirectory.detail.version")}
            value={item.version}
            mono
          />
          <Metadata
            label={t("capabilities.mcpDirectory.detail.transport")}
            value={item.transport}
            mono
          />
          <Metadata
            label={t("capabilities.mcpDirectory.detail.authentication")}
            value={t("capabilities.mcpDirectory.detail.noAuthentication")}
          />
        </div>
        <div className="mt-5 grid gap-4 lg:grid-cols-[minmax(0,1fr)_260px]">
          <div className="space-y-4">
            <section>
              <h3 className="text-sm font-medium text-fg">
                {t("capabilities.mcpDirectory.detail.endpoint")}
              </h3>
              <pre className="mt-2 overflow-x-auto rounded-lg border border-line bg-surface-muted/35 p-3 font-mono text-xs leading-5 text-fg">
                {item.url}
              </pre>
            </section>
            <div className="rounded-lg border border-line bg-surface-muted/25 p-4 text-sm leading-5 text-fg-muted">
              <div className="flex items-start gap-2">
                <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-fg-subtle" />
                <span>{t("capabilities.mcpDirectory.securityNotice")}</span>
              </div>
            </div>
          </div>
          <aside className="space-y-3 rounded-lg border border-line bg-surface-muted/20 p-4">
            <ExternalLinkRow
              label={t("capabilities.mcpDirectory.detail.publisher")}
              value={item.publisher.name}
              href={item.publisher.url}
            />
            <ExternalLinkRow
              label={t("capabilities.mcpDirectory.detail.homepage")}
              value={t("capabilities.mcpDirectory.detail.openLink")}
              href={item.homepage_url}
            />
            <ExternalLinkRow
              label={t("capabilities.mcpDirectory.detail.repository")}
              value={t("capabilities.mcpDirectory.detail.openLink")}
              href={item.repository_url}
            />
          </aside>
        </div>
        <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
          {item.installed && item.installed_capability_id ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => onViewCapability(item.installed_capability_id!)}
            >
              {t("capabilities.mcpDirectory.actions.viewCapability")}
            </Button>
          ) : (
            <Button
              size="sm"
              disabled={!canImport}
              title={!canImport ? t("capabilities.permission.adminOnly") : undefined}
              onClick={onImport}
            >
              {canImport
                ? t("capabilities.mcpDirectory.actions.import")
                : t("capabilities.permission.adminOnly")}
            </Button>
          )}
        </div>
      </article>
    </div>
  )
}
