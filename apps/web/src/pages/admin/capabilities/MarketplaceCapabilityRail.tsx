import { useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight, PackageCheck } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { DetailRail, RailSection } from "../../../components/ui/detail-rail"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { InitialTile, Ledger, LedgerRow, col } from "../../../components/ui/ledger"
import { PropertyList, Property } from "../../../components/ui/property-list"
import { Skeleton } from "../../../components/ui/skeleton"
import { useTargetMarketplaceInstalls, useMarketplaceEnabledAgents, useUninstall, type TargetMarketplaceInstall, marketplaceSourceName } from "../../../lib/api-marketplace"
import { navigateAdmin } from "../../../lib/admin-router"
import { useWorkspaceId } from "../../../lib/workspace"
import { requiredCredentialsLabel } from "../capability-ui"
import { CapabilityTypeBadge } from "./CapabilityTypeBadge"
import { InlineNotice } from "./notices"
import { UninstallMarketplaceDialog } from "./UninstallMarketplaceDialog"

/** agent · version · open */
const AGENT_COLUMNS = [col.title(), col.id(120), col.icon()]

/**
 * A capability installed from another workspace, read in the rail beside the
 * list. Same shape as its workspace-owned sibling; only the verbs differ.
 */
export function MarketplaceCapabilityRail({ id, open, onClose, onClosed }: {
  id: string
  open: boolean
  onClose: () => void
  onClosed: () => void
}) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const installsQ = useTargetMarketplaceInstalls(workspaceID)
  const agentsQ = useMarketplaceEnabledAgents(workspaceID, id)
  const uninstallMut = useUninstall(workspaceID)
  const [uninstallOpen, setUninstallOpen] = useState(false)
  // Swap content rather than replay the entrance when the rail moves on.
  const [shownID, setShownID] = useState(id)
  if (id !== shownID) {
    setShownID(id)
    setUninstallOpen(false)
  }
  const capability = useMemo(() => (installsQ.data ?? []).find((item) => item.id === id) ?? null, [installsQ.data, id])
  const agents = agentsQ.data ?? capability?.enabled_agents ?? []
  const closeLabel = t("capabilities.detail.backToList")

  if (installsQ.isLoading || installsQ.error || !capability) {
    return (
      <DetailRail
        open={open}
        onClose={onClose}
        onClosed={onClosed}
        closeLabel={closeLabel}
        aria-label={t("capabilities.page.title")}
        header={<Skeleton className="h-3 w-40" />}
      >
        {installsQ.isLoading ? (
          <div className="space-y-3">{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-3 w-full" />)}</div>
        ) : installsQ.error ? (
          <ErrorState
            title={t("capabilities.marketplaceDetail.loadError.title")}
            description={installsQ.error instanceof Error ? installsQ.error.message : t("capabilities.marketplaceDetail.loadError.description")}
            onRetry={() => void installsQ.refetch()}
          />
        ) : (
          <EmptyState
            icon={PackageCheck}
            title={t("capabilities.marketplaceDetail.notFound.title")}
            description={t("capabilities.marketplaceDetail.notFound.description")}
          />
        )}
      </DetailRail>
    )
  }

  const source = marketplaceSourceName(capability)
  const deprecated = !!capability.deprecated_at
  const latest = capability.latest_version ?? capability.latest_published_version
  const agentCount = agents.length || capability.enabled_agent_count

  return (
    <DetailRail
      open={open}
      onClose={onClose}
      onClosed={onClosed}
      closeLabel={closeLabel}
      aria-label={capability.name}
      header={
        <>
          <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{capability.name}</span>
          <CapabilityTypeBadge type={capability.type} />
          {deprecated ? (
            <Badge variant="neutral" dot>{t("capabilities.deprecated.badgeTarget")}</Badge>
          ) : (
            <Badge variant="neutral" dot>{t("capabilities.marketplaceDetail.badge")}</Badge>
          )}
        </>
      }
      footer={<Button variant="outline" onClick={() => setUninstallOpen(true)}>{t("capabilities.uninstall.action")}</Button>}
    >
      <>
        {deprecated && <InlineNotice tone="warning" className="mb-4">{t("capabilities.deprecated.bannerTarget")}</InlineNotice>}
        {capability.description && <p className="mb-4 text-sm text-fg">{capability.description}</p>}

        <RailSection title={t("capabilities.marketplaceDetail.source.title")}>
          <PropertyList>
            <Property label={t("capabilities.marketplaceDetail.source.workspace")}>{source || t("capabilities.none")}</Property>
            <Property label={t("capabilities.marketplaceDetail.source.pinnedVersion")} mono>{capability.pinned_version ? `v${capability.pinned_version}` : t("capabilities.none")}</Property>
            <Property label={t("capabilities.marketplaceDetail.source.latestVersion")} mono>{latest ? `v${latest}` : t("capabilities.none")}</Property>
            <Property label={t("capabilities.table.credentials")}>{requiredCredentialsLabel(capability.required_credentials, i18n.language, t("capabilities.credentials.none"))}</Property>
          </PropertyList>
        </RailSection>

        <RailSection title={t("capabilities.marketplaceDetail.enabledAgents.title", { count: agentCount })} className="mt-6">
          {agentsQ.isLoading ? (
            <Skeleton className="mt-2 h-3 w-full" />
          ) : agents.length === 0 ? (
            <p className="pt-1 text-sm text-fg-muted">{t("capabilities.marketplaceDetail.enabledAgents.empty")}</p>
          ) : (
            <Ledger columns={AGENT_COLUMNS} className="-mx-4" role="listbox" aria-label={t("capabilities.marketplaceDetail.enabledAgents.title", { count: agentCount })}>
              <ul className="m-0 list-none p-0">
                {agents.map((agent) => {
                  const agentID = agent.agent_id ?? agent.id
                  const name = agent.name ?? agent.agent_name ?? "—"
                  const openAgent = () => agentID && navigateAdmin("agents", { id: agentID, tab: "capabilities" })
                  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      openAgent()
                    }
                  }
                  return (
                    <LedgerRow key={agentID ?? name} onClick={openAgent} onKeyDown={onKeyDown}>
                      <span className="flex min-w-0 items-center gap-1.5">
                        <InitialTile name={name} />
                        <span className="truncate font-medium">{name}</span>
                      </span>
                      <span className="truncate font-mono text-xs text-fg">v{agent.version ?? capability.pinned_version ?? "—"}</span>
                      <ArrowUpRight className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                    </LedgerRow>
                  )
                })}
              </ul>
            </Ledger>
          )}
        </RailSection>
      </>

      <UninstallMarketplaceDialog
        capability={capability as TargetMarketplaceInstall}
        agents={agents}
        open={uninstallOpen}
        pending={uninstallMut.isPending}
        error={uninstallMut.error}
        onOpenChange={(open) => {
          setUninstallOpen(open)
          if (!open) uninstallMut.reset()
        }}
        onConfirm={() => uninstallMut.mutate(capability.id, { onSuccess: () => navigateAdmin("capabilities") })}
      />
    </DetailRail>
  )
}
