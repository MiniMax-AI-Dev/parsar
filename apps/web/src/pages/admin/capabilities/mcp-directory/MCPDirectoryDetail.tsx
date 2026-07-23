import { ArrowLeft, Check, Loader2, Server, ShieldCheck } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { ConnectorIcon, ExternalLinkRow, Metadata, VerifiedBadge } from "./shared"
import { formatCommandPart, isConnectorConnectionActive } from "./utils"

export function DirectoryDetail({
  item,
  loading,
  error,
  canImport,
  onBack,
  onRetry,
  onImport,
  onConnect,
  onTestConnection,
  testingConnection,
  connectionTestSucceeded,
  connectionTestFailed,
  connectionTestError,
  onViewCapability,
  onAddToAgent,
}: {
  item: MCPDirectoryItem | null
  loading: boolean
  error: unknown
  canImport: boolean
  onBack: () => void
  onRetry: () => void
  onImport: () => void
  onConnect: () => void
  onTestConnection: () => void
  testingConnection: boolean
  connectionTestSucceeded: boolean
  connectionTestFailed: boolean
  connectionTestError: unknown
  onViewCapability: (capabilityID: string) => void
  onAddToAgent: (capabilityID: string) => void
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
  const command = [item.command ?? "", ...(item.args ?? [])]
    .filter(Boolean)
    .map(formatCommandPart)
    .join(" ")
  const isRemote = item.transport === "streamable-http"
  const oauthStatus = item.connection_status ?? (item.connected ? "authorized" : "not_connected")
  const connectionVerified = oauthStatus === "verified"
  const connectionHealthy = connectionVerified || connectionTestSucceeded
  const connectionActive = isConnectorConnectionActive(item)
  const connectionUnavailable =
    item.authentication === "oauth2" && item.connection_supported === false
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
              {connectionActive ? (
                <Badge variant="success">
                  {t(
                    connectionVerified
                      ? "capabilities.mcpDirectory.oauth.verified"
                      : "capabilities.mcpDirectory.oauth.connected",
                  )}
                </Badge>
              ) : null}
              {connectionUnavailable ? (
                <Badge variant="neutral">
                  {t("capabilities.mcpDirectory.actions.unavailable")}
                </Badge>
              ) : null}
            </div>
            <p className="mt-1 text-sm text-fg-subtle">{item.publisher.name}</p>
            <p className="mt-4 max-w-3xl text-sm leading-6 text-fg-muted">{item.description}</p>
          </div>
        </div>
        <div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
            label={
              isRemote
                ? t("capabilities.mcpDirectory.detail.authentication")
                : t("capabilities.mcpDirectory.detail.timeout")
            }
            value={
              isRemote
                ? connectionUnavailable
                  ? t("capabilities.mcpDirectory.actions.unavailable")
                  : item.authentication === "oauth2"
                    ? connectionVerified
                      ? t("capabilities.mcpDirectory.oauth.verified")
                      : item.connected
                        ? t("capabilities.mcpDirectory.oauth.authorized")
                        : t("capabilities.mcpDirectory.oauth.required")
                    : t("capabilities.mcpDirectory.detail.noAuthentication")
                : t("capabilities.mcpDirectory.detail.seconds", {
                    count: item.startup_timeout_sec ?? 0,
                  })
            }
          />
        </div>
        <div className="mt-5 grid gap-4 lg:grid-cols-[minmax(0,1fr)_260px]">
          <div className="space-y-4">
            <section>
              <h3 className="text-sm font-medium text-fg">
                {t(
                  isRemote
                    ? "capabilities.mcpDirectory.detail.endpoint"
                    : "capabilities.mcpDirectory.detail.command",
                )}
              </h3>
              <pre className="mt-2 overflow-x-auto rounded-lg border border-line bg-surface-muted/35 p-3 font-mono text-xs leading-5 text-fg">
                {isRemote ? item.url : command}
              </pre>
            </section>
            {!isRemote ? (
              <section>
                <h3 className="text-sm font-medium text-fg">
                  {t("capabilities.mcpDirectory.detail.environment")}
                </h3>
                {(item.env ?? []).length ? (
                  <div className="mt-2 divide-y divide-line rounded-lg border border-line">
                    {item.env?.map((name) => (
                      <div key={name} className="px-3 py-2 font-mono text-xs text-fg">
                        {name}
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="mt-2 text-sm text-fg-subtle">
                    {t("capabilities.mcpDirectory.detail.noEnvironment")}
                  </p>
                )}
              </section>
            ) : null}
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
        {connectionUnavailable ? (
          <div className="mt-5 rounded-lg border border-line bg-surface-muted/25 px-4 py-3 text-sm text-fg-muted">
            {t("capabilities.mcpDirectory.oauth.approvedClientRequired", { name: item.name })}
          </div>
        ) : null}
        {connectionTestError || connectionTestFailed ? (
          <p className="mt-4 text-sm text-danger-emphasis">
            {t("capabilities.mcpDirectory.oauth.testFailed")}
          </p>
        ) : null}
        <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
          {!connectionUnavailable && item.authentication === "oauth2" && item.connected ? (
            <Button
              variant="outline"
              size="sm"
              className={
                connectionHealthy
                  ? "border-success-border bg-success-subtle text-success-emphasis hover:border-success-border hover:bg-success-subtle"
                  : undefined
              }
              onClick={onTestConnection}
              disabled={testingConnection}
            >
              {testingConnection ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : connectionHealthy ? (
                <Check className="h-3.5 w-3.5" />
              ) : null}
              {testingConnection
                ? t("capabilities.mcpDirectory.oauth.testing")
                : connectionHealthy
                  ? t("capabilities.mcpDirectory.oauth.testSuccess")
                  : t("capabilities.mcpDirectory.oauth.test")}
            </Button>
          ) : null}
          {!connectionUnavailable && item.authentication === "oauth2" ? (
            <Button variant="outline" size="sm" onClick={onConnect} disabled={testingConnection}>
              {item.connected
                ? t("capabilities.mcpDirectory.oauth.reconnect")
                : t("capabilities.mcpDirectory.oauth.connect", { name: item.name })}
            </Button>
          ) : null}
          {item.installed && item.installed_capability_id ? (
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => onViewCapability(item.installed_capability_id!)}
              >
                {t("capabilities.mcpDirectory.actions.viewCapability")}
              </Button>
              {!connectionUnavailable ? (
                <Button size="sm" onClick={() => onAddToAgent(item.installed_capability_id!)}>
                  {t("capabilities.mcpDirectory.actions.addToAgent")}
                </Button>
              ) : null}
            </>
          ) : (
            <Button
              size="sm"
              disabled={!canImport || connectionUnavailable}
              title={
                connectionUnavailable
                  ? t("capabilities.mcpDirectory.oauth.approvedClientRequired", {
                      name: item.name,
                    })
                  : !canImport
                    ? t("capabilities.permission.adminOnly")
                    : undefined
              }
              onClick={onImport}
            >
              {connectionUnavailable
                ? t("capabilities.mcpDirectory.actions.unavailable")
                : canImport
                  ? t("capabilities.mcpDirectory.actions.import")
                  : t("capabilities.permission.adminOnly")}
            </Button>
          )}
        </div>
      </article>
    </div>
  )
}
