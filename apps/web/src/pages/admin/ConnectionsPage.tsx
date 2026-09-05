import { useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { DiscordConnectorFields } from "../../components/admin/channel-connectors/discordFields"
import { FeishuConnectorFields } from "../../components/admin/channel-connectors/feishuFields"
import { SlackConnectorFields } from "../../components/admin/channel-connectors/slackFields"
import { TeamsConnectorFields } from "../../components/admin/channel-connectors/teamsFields"
import { DetailRail } from "../../components/ui/detail-rail"
import { ErrorState } from "../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerId, LedgerRow } from "../../components/ui/ledger"
import { Skeleton } from "../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../components/ui/status-icon"
import {
  readDiscordConnector,
  readFeishuConnector,
  readSlackConnector,
  readTeamsConnector,
  useWorkspaceIMConnectors,
  type ConnectorPlatform,
  type DiscordConnectorInput,
  type FeishuConnectorInput,
  type SlackConnectorInput,
  type TeamsConnectorInput,
} from "../../lib/api-connectors"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useRelativeTime } from "../../lib/relative-time"
import { useWorkspaceId } from "../../lib/workspace"

const PLATFORMS: ConnectorPlatform[] = ["feishu", "slack", "discord", "teams"]

type ConnectorStatus = "enabled" | "configured" | "incomplete" | "notConfigured"

type ConnectorConfig =
  | FeishuConnectorInput
  | SlackConnectorInput
  | DiscordConnectorInput
  | TeamsConnectorInput

/* Connector state drawn with the run-status vocabulary: enabled is a
   completed disc, configured-but-off a queued ring, incomplete an
   interrupted ring, not configured a cancelled ring. */
const STATUS_ICON: Record<ConnectorStatus, StatusKind> = {
  enabled: "completed",
  configured: "queued",
  incomplete: "interrupted",
  notConfigured: "cancelled",
}

/** status icon · platform · app id · state · updated */
const LEDGER_COLUMNS = "14px minmax(0,1fr) 220px 128px 80px"

export function ConnectionsPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const wsId = useWorkspaceId()
  const fmtAgo = useRelativeTime()
  const { data: myWorkspaces } = useMyWorkspaces()
  const connectorsQ = useWorkspaceIMConnectors(wsId)
  const connectors = connectorsQ.data?.connectors
  const [selected, setSelected] = useState<ConnectorPlatform | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  const canEdit = useMemo(() => {
    const role = myWorkspaces?.workspaces.find((w) => w.id === wsId)?.role
    return role === "owner" || role === "admin"
  }, [myWorkspaces, wsId])

  const configs = useMemo(
    () => ({
      feishu: readFeishuConnector(connectors),
      slack: readSlackConnector(connectors),
      discord: readDiscordConnector(connectors),
      teams: readTeamsConnector(connectors),
    }),
    [connectors],
  )

  const rows = useMemo(
    () =>
      PLATFORMS.map((platform) => {
        const config = configs[platform]
        const raw = connectors?.find((c) => c.platform === platform)
        return {
          platform,
          appID: config?.app_id.trim() ?? "",
          status: platformStatus(platform, config),
          updatedAt: raw?.updated_at,
        }
      }),
    [configs, connectors],
  )

  // Saved confirmations are transient: one inline line in the header.
  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), 4000)
    return () => window.clearTimeout(timer)
  }, [toast])

  const pageTitle = t("connections.page.title")
  const platformName = (p: ConnectorPlatform) => t(`connections.connector.platformSelect.options.${p}`)
  const err = connectorsQ.error
  const selectedRow = rows.find((r) => r.platform === selected)

  return (
    <AdminLayout activeMenu="connections" fullBleed>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <PageHeader
            className="static mx-0 mb-0"
            title={pageTitle}
            subtitleFor="connections.page.title"
            action={
              toast ? (
                <span className="flex items-center gap-1.5 text-sm text-fg animate-pop-in data-[state=closed]:animate-pop-out" role="status">
                  <StatusIcon status="completed" />
                  {toast}
                </span>
              ) : undefined
            }
          />

          {!wsId ? (
            <ScopeRequiredState scope="workspace" resourceName={pageTitle} />
          ) : connectorsQ.isLoading ? (
            <div className="px-4 pt-3">
              <div className="mb-3 h-7 border-b border-line" />
              {PLATFORMS.map((p) => (
                <div key={p} className="flex h-9 items-center gap-3 border-b border-line">
                  <Skeleton className="h-3.5 w-3.5 rounded-full" />
                  <Skeleton className="h-3 w-24" />
                  <Skeleton className="h-3 flex-1" />
                  <Skeleton className="h-3 w-16" />
                </div>
              ))}
            </div>
          ) : err ? (
            <div className="px-4 pt-4">
              <ErrorState
                title={tc("states.errorTitle")}
                description={err instanceof Error ? err.message : String(err)}
                onRetry={() => void connectorsQ.refetch()}
              />
            </div>
          ) : (
            <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
              <LedgerHeader>
                <span />
                <span>{t("connections.connector.platformSelect.label")}</span>
                <span>App ID</span>
                <span>{t("connectors.table.status")}</span>
                <span />
              </LedgerHeader>
              <ul className="m-0 list-none p-0">
                {rows.map((row) => {
                  const statusLabel = t(`connections.connector.platformList.status.${row.status}`)
                  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      setSelected(row.platform)
                    }
                  }
                  return (
                    <LedgerRow
                      key={row.platform}
                      selected={selected === row.platform}
                      onClick={() => setSelected(row.platform)}
                      onKeyDown={onKeyDown}
                    >
                      <StatusIcon status={STATUS_ICON[row.status]} title={statusLabel} />
                      <span className="truncate font-medium">{platformName(row.platform)}</span>
                      <LedgerId>{row.appID || "—"}</LedgerId>
                      <span className="truncate">{statusLabel}</span>
                      <span className="truncate text-right text-xs text-fg-muted">
                        {row.updatedAt ? fmtAgo(row.updatedAt) : "—"}
                      </span>
                    </LedgerRow>
                  )
                })}
              </ul>
            </Ledger>
          )}
        </div>

        {wsId && selected && selectedRow && (
          <DetailRail
            key={selected}
            aria-label={platformName(selected)}
            onClose={() => setSelected(null)}
            closeLabel={t("connections.detail.close")}
            header={
              <>
                <StatusIcon status={STATUS_ICON[selectedRow.status]} />
                <span className="shrink-0 text-sm font-medium text-fg">{platformName(selected)}</span>
                <LedgerId className="min-w-0 flex-1">{selectedRow.appID || "—"}</LedgerId>
              </>
            }
          >
            {selected === "feishu" && (
              <FeishuConnectorFields
                workspaceID={wsId}
                current={configs.feishu}
                masterKeyConfigured={connectorsQ.data?.master_key_configured}
                canEdit={canEdit}
                onToast={setToast}
              />
            )}
            {selected === "slack" && (
              <SlackConnectorFields workspaceID={wsId} current={configs.slack} canEdit={canEdit} onToast={setToast} />
            )}
            {selected === "discord" && (
              <DiscordConnectorFields workspaceID={wsId} current={configs.discord} canEdit={canEdit} onToast={setToast} />
            )}
            {selected === "teams" && (
              <TeamsConnectorFields workspaceID={wsId} current={configs.teams} canEdit={canEdit} onToast={setToast} />
            )}
          </DetailRail>
        )}
      </div>
    </AdminLayout>
  )
}

/* ------------------------------------------------------------------ */
/*  State mapping (mirrors the server's completeness rules per platform) */
/* ------------------------------------------------------------------ */

function platformStatus(platform: ConnectorPlatform, config: ConnectorConfig | undefined): ConnectorStatus {
  if (!config?.app_id.trim()) return "notConfigured"
  if (!isPlatformComplete(platform, config)) return "incomplete"
  return config.enabled ? "enabled" : "configured"
}

function isPlatformComplete(platform: ConnectorPlatform, config: ConnectorConfig): boolean {
  switch (platform) {
    case "feishu": {
      const c = config as FeishuConnectorInput
      return Boolean(
        c.app_secret_ref.trim() &&
        (c.event_mode === "websocket" || c.verification_token_ref.trim()),
      )
    }
    case "slack": {
      const c = config as SlackConnectorInput
      return Boolean(
        c.bot_token_ref.trim() &&
        (c.event_mode === "socket" ? c.app_token_ref.trim() : c.signing_secret_ref.trim()),
      )
    }
    case "discord": {
      const c = config as DiscordConnectorInput
      return Boolean(c.bot_token_ref.trim() && c.public_key_ref.trim())
    }
    case "teams": {
      const c = config as TeamsConnectorInput
      return Boolean(c.app_password_ref.trim())
    }
  }
}
