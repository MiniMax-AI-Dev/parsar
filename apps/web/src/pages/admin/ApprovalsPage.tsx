import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Inbox } from "lucide-react"

import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { InteractionDecisionCard } from "../../components/admin/InteractionDecisionCard"
import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { Badge } from "../../components/ui/badge"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Skeleton } from "../../components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import { type InteractionStatusGroup, useAgentInteractions } from "../../lib/api-interactions"
import type { AgentInteraction } from "../../lib/api-types"
import { firstInteractionQuestion } from "../../lib/interaction-questions"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"
import { useWorkspaceId } from "../../lib/workspace"

export function ApprovalsPage() {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const [tab, setTab] = useState<InteractionStatusGroup>("pending")
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const query = useAgentInteractions(workspaceID, tab)
  const rows = useMemo(() => query.data?.interactions ?? [], [query.data])

  const selected = rows.find((row) => row.id === selectedID) ?? rows[0] ?? null
  const error = query.error
  const unreachable = error instanceof ApiError && error.envelope.unreachable

  return (
    <AdminLayout activeMenu="approvals">
      <PageHeader title={t("approvals.page.title")} description={t("approvals.page.description")} />
      {!workspaceID ? (
        <ScopeRequiredState scope="workspace" resourceName={t("approvals.page.title")} />
      ) : error ? (
        <ErrorState
          title={
            unreachable
              ? t("approvals.loadError.unreachable.title")
              : t("approvals.loadError.title")
          }
          description={
            unreachable
              ? t("approvals.loadError.unreachable.description")
              : error instanceof Error
                ? error.message
                : t("approvals.loadError.description")
          }
          hint={
            unreachable ? t("approvals.loadError.unreachable.hint") : t("approvals.loadError.hint")
          }
          onRetry={() => void query.refetch()}
        />
      ) : (
        <div className="space-y-4">
          <Tabs value={tab} onValueChange={(value) => setTab(value as InteractionStatusGroup)}>
            <TabsList>
              <TabsTrigger value="pending">{t("approvals.tabs.pending")}</TabsTrigger>
              <TabsTrigger value="decided">{t("approvals.tabs.decided")}</TabsTrigger>
              <TabsTrigger value="expired">{t("approvals.tabs.expired")}</TabsTrigger>
            </TabsList>
          </Tabs>
          {query.isLoading ? (
            <div className="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.35fr)]">
              <Skeleton className="h-80" />
              <Skeleton className="h-80" />
            </div>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={Inbox}
              title={t("approvals.empty.title")}
              description={t("approvals.empty.description")}
            />
          ) : (
            <div className="grid min-h-[520px] overflow-hidden rounded-xl border border-line bg-surface lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.35fr)]">
              <div className="border-b border-line lg:border-b-0 lg:border-r">
                {rows.map((row) => (
                  <InteractionListItem
                    key={row.id}
                    row={row}
                    selected={row.id === selected?.id}
                    onSelect={() => setSelectedID(row.id)}
                  />
                ))}
              </div>
              {selected ? (
                <InteractionDecisionCard
                  key={selected.id}
                  interaction={selected}
                  workspaceID={workspaceID}
                />
              ) : null}
            </div>
          )}
        </div>
      )}
    </AdminLayout>
  )
}

function InteractionListItem({
  row,
  selected,
  onSelect,
}: {
  row: AgentInteraction
  selected: boolean
  onSelect: () => void
}) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const title =
    row.kind === "permission"
      ? String(row.request.resource || row.request.action || t("approvals.kind.permission"))
      : firstInteractionQuestion(row)?.question || t("approvals.kind.userChoice")
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "w-full border-b border-line px-4 py-4 text-left transition-colors hover:bg-surface-muted",
        selected && "bg-info-subtle/50",
      )}
    >
      <div className="mb-2 flex items-center justify-between gap-3">
        <Badge variant={row.kind === "permission" ? "warning" : "primary"}>
          {t(`approvals.kind.${row.kind === "permission" ? "permission" : "userChoice"}`)}
        </Badge>
        <span className="text-xs text-fg-faint">{fmtAgo(row.created_at)}</span>
      </div>
      <p className="line-clamp-2 text-sm font-semibold text-fg">{title}</p>
      <p className="mt-1 truncate text-xs text-fg-subtle">{row.agent_name || row.agent_run_id}</p>
    </button>
  )
}
