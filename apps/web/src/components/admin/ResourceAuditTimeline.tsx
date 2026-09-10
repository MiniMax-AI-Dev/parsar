import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Bot, Code, Cog, Globe, User as UserIcon, type LucideIcon } from "lucide-react"

import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { ErrorState } from "../ui/error-state"
import { Skeleton } from "../ui/skeleton"
import { VerbatimBlock } from "../ui/verbatim"
import { useAuditRecords } from "../../lib/api-governance"
import type { AuditActorType } from "../../lib/api-types"
import { useReadableAuditRecords, type ReadableAuditRecord } from "../../lib/audit-presentation"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"

const ACTOR_ICON: Record<AuditActorType, LucideIcon> = {
  agent: Bot,
  user: UserIcon,
  external: Globe,
  system: Cog,
}

function TimelineRow({ record, fmtAgo }: { record: ReadableAuditRecord; fmtAgo: (iso: string | null | undefined) => string }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const Icon = ACTOR_ICON[record.actor_type] ?? Cog
  const detailLabel = t("audit.detail.event")

  return (
    <li className="border-b border-line last:border-b-0">
      <div className="flex items-start gap-2 py-2 text-sm">
        <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <div className="min-w-0 flex-1 [overflow-wrap:anywhere]">
          <div className="text-fg" title={record.event_type}>{record.eventLabel}</div>
          <div className="text-xs text-fg-muted" title={record.actorDetail}>{record.actorLabel}</div>
        </div>
        <span className="shrink-0 text-xs text-fg-muted" title={record.occurred_at}>
          {fmtAgo(record.occurred_at)}
        </span>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6 shrink-0"
          aria-expanded={open}
          aria-label={detailLabel}
          title={detailLabel}
          onClick={() => setOpen((v) => !v)}
        >
          <Code className={cn(open && "text-fg")} strokeWidth={1.5} />
        </Button>
      </div>
      {open && (
        <VerbatimBlock className="mb-2 mt-0">
          {`#${record.id} ${record.event_type}\n${record.source} · ${record.actorDetail}\n${record.occurred_at}\n${JSON.stringify(record.payload ?? {}, null, 2)}`}
        </VerbatimBlock>
      )}
    </li>
  )
}

export interface ResourceAuditTimelineProps {
  /** Active workspace ID; null surfaces mock data on dev landing. */
  wsId: string | null
  /** Resource discriminator the feed pins to (`agent_run`, `agent`, …). */
  targetType: string
  /** Required — without an ID we'd query the unfiltered workspace feed. */
  targetID: string
  /** Override the default 200-row cap. */
  limit?: number
}

export function ResourceAuditTimeline({
  wsId,
  targetType,
  targetID,
  limit,
}: ResourceAuditTimelineProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const query = useAuditRecords(wsId, {
    target_type: targetType,
    target_id: targetID,
    limit,
  })

  const records = useReadableAuditRecords(wsId, query.data?.audit_records)

  if (query.isLoading) {
    return (
      <div className="space-y-2 pt-2">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-3/4" />
        <Skeleton className="h-4 w-5/6" />
      </div>
    )
  }
  if (query.isError) {
    return (
      <ErrorState
        title={t("audit.loadError.title", { defaultValue: "Failed to load audit records" })}
        detail={query.error instanceof Error ? query.error.message : String(query.error)}
        onRetry={() => query.refetch()}
      />
    )
  }
  if (records.length === 0) {
    return (
      <EmptyState
        title={t("audit.resourceTimeline.empty.title", { defaultValue: "No audit events yet" })}
        description={t("audit.resourceTimeline.empty.description", {
          defaultValue: "This resource has not produced any audit records.",
        })}
        size="compact"
      />
    )
  }
  // Defensive re-sort: API returns newest-first, but a future cache
  // layer or mock could reorder.
  const sorted = [...records].sort((a, b) => b.occurred_at.localeCompare(a.occurred_at))
  return (
    <ul className="m-0 list-none p-0">
      {sorted.map((r) => <TimelineRow key={r.id} record={r} fmtAgo={fmtAgo} />)}
    </ul>
  )
}
