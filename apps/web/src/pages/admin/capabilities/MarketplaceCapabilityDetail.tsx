import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowLeft, ArrowUpRight, PackageCheck } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Skeleton } from "../../../components/ui/skeleton"
import { useTargetMarketplaceInstalls, useMarketplaceEnabledAgents, useUninstall, type TargetMarketplaceInstall, marketplaceSourceName } from "../../../lib/api-marketplace"
import { navigateAdmin } from "../../../lib/admin-router"
import { useWorkspaceId } from "../../../lib/workspace"
import { requiredCredentialsLabel } from "../capability-ui"
import type { Capability } from "../../../lib/api-types"
import { UninstallMarketplaceDialog } from "./UninstallMarketplaceDialog"

export function MarketplaceCapabilityDetail({ id }: { id: string }) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const installsQ = useTargetMarketplaceInstalls(workspaceID)
  const agentsQ = useMarketplaceEnabledAgents(workspaceID, id)
  const uninstallMut = useUninstall(workspaceID)
  const [uninstallOpen, setUninstallOpen] = useState(false)
  const capability = useMemo(() => (installsQ.data ?? []).find((item) => item.id === id) ?? null, [installsQ.data, id])
  const agents = agentsQ.data ?? capability?.enabled_agents ?? []

  if (installsQ.isLoading) return <div className="space-y-4"><Skeleton className="h-16 w-full" /><Skeleton className="h-40 w-full" /></div>

  if (installsQ.error) {
    return <ErrorState title={t("capabilities.marketplaceDetail.loadError.title")} description={installsQ.error instanceof Error ? installsQ.error.message : t("capabilities.marketplaceDetail.loadError.description")} onRetry={() => void installsQ.refetch()} />
  }

  if (!capability) {
    return <EmptyState icon={PackageCheck} title={t("capabilities.marketplaceDetail.notFound.title")} description={t("capabilities.marketplaceDetail.notFound.description")} action={<Button variant="outline" size="sm" onClick={() => navigateAdmin("capabilities")}>{t("capabilities.detail.backToList")}</Button>} />
  }

  const source = marketplaceSourceName(capability)
  const deprecated = !!capability.deprecated_at

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <button onClick={() => navigateAdmin("capabilities")} className="inline-flex items-center gap-1 text-[13px] text-slate-500 hover:text-slate-900 hover:underline"><ArrowLeft className="h-3 w-3" />{t("capabilities.detail.backToList")}</button>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <h2 className="text-[22px] font-semibold tracking-display text-slate-900">{capability.name}</h2>
            <CapabilityTypeBadge type={capability.type} />
            <Badge variant="primary">{t("capabilities.marketplaceDetail.badge")}</Badge>
            {deprecated && <Badge variant="destructive">{t("capabilities.deprecated.badgeTarget")}</Badge>}
          </div>
          <p className="mt-1 text-[13px] text-slate-500">{capability.description || t("capabilities.detail.noDescription")}</p>
        </div>
      </div>

      {deprecated && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[13px] leading-5 text-red-800">
          <AlertTriangle className="mr-1 inline h-3.5 w-3.5" />{t("capabilities.deprecated.bannerTarget")}
        </div>
      )}

      <Card title={t("capabilities.marketplaceDetail.source.title")}>
        <div className="grid gap-3 md:grid-cols-4">
          <Detail label={t("capabilities.marketplaceDetail.source.workspace")} value={source || t("capabilities.none")} />
          <Detail label={t("capabilities.marketplaceDetail.source.pinnedVersion")} value={capability.pinned_version ? `v${capability.pinned_version}` : t("capabilities.none")} mono />
          <Detail label={t("capabilities.marketplaceDetail.source.latestVersion")} value={capability.latest_version || capability.latest_published_version ? `v${capability.latest_version ?? capability.latest_published_version}` : t("capabilities.none")} mono />
          <Detail label={t("capabilities.table.credentials")} value={requiredCredentialsLabel(capability.required_credentials, i18n.language, t("capabilities.credentials.none"))} />
        </div>
      </Card>

      <Card title={t("capabilities.marketplaceDetail.enabledAgents.title", { count: agents.length || capability.enabled_agent_count })}>
        {agentsQ.isLoading ? <Skeleton className="h-12 w-full" /> : agents.length === 0 ? (
          <p className="text-[13px] text-slate-500">{t("capabilities.marketplaceDetail.enabledAgents.empty")}</p>
        ) : (
          <div className="space-y-2">
            {agents.map((agent) => {
              const id = agent.project_agent_id ?? agent.agent_id ?? agent.id
              return (
                <button key={id ?? agent.name} type="button" onClick={() => id && navigateAdmin("agents", { id, tab: "capabilities" })} className="flex w-full items-center justify-between rounded-md border border-slate-200 p-3 text-left hover:bg-slate-50">
                  <span className="text-[13px] font-medium text-slate-900">{agent.name ?? agent.agent_name ?? "—"}</span>
                  <span className="flex items-center gap-2 text-[13px] text-slate-500"><span className="font-mono">v{agent.version ?? capability.pinned_version ?? "—"}</span><ArrowUpRight className="h-3.5 w-3.5" /></span>
                </button>
              )
            })}
          </div>
        )}
      </Card>

      <section className="rounded-lg border border-red-200 bg-red-50/40 p-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h3 className="text-[13px] font-semibold text-red-900">{t("capabilities.uninstall.sectionTitle")}</h3>
            <p className="mt-1 text-[13px] text-red-700">{t("capabilities.uninstall.sectionDescription")}</p>
          </div>
          <Button variant="destructive" size="sm" onClick={() => setUninstallOpen(true)}>{t("capabilities.uninstall.action")}</Button>
        </div>
      </section>

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
    </div>
  )
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="rounded-lg border border-slate-200 bg-white p-4"><h3 className="mb-3 text-[13px] font-semibold uppercase tracking-wider text-slate-500">{title}</h3>{children}</section>
}

function Detail({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return <div className="rounded-md border border-slate-200 bg-white p-3"><p className="text-[12px] text-slate-500">{label}</p><div className={`mt-1 text-[13px] text-slate-900 ${mono ? "font-mono" : ""}`}>{value}</div></div>
}

function CapabilityTypeBadge({ type }: { type: Capability["type"] }) {
  if (type === "skill") return <Badge variant="primary">Skill</Badge>
  if (type === "plugin") return <Badge variant="success">Plugin</Badge>
  return <Badge variant="neutral">MCP</Badge>
}
