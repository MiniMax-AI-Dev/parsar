import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Bot, Code, Cog, Globe, User as UserIcon, type LucideIcon } from "lucide-react"

import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { ErrorState } from "../ui/error-state"
import { Skeleton } from "../ui/skeleton"
import { useAuditRecords } from "../../lib/api-governance"
import type { AuditActorType, AuditRecord } from "../../lib/api-types"
import { useRelativeTime } from "../../lib/relative-time"
import { cn } from "../../lib/utils"

const ACTOR_ICON: Record<AuditActorType, LucideIcon> = {
  agent: Bot,
  user: UserIcon,
  external: Globe,
  system: Cog,
}

function shortId(s: string | undefined | null, n = 10): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}

/** One hairline-separated 32px row, the same idiom as the run steps list. */
function TimelineRow({ record, fmtAgo }: { record: AuditRecord; fmtAgo: (iso: string | null | undefined) => string }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const Icon = ACTOR_ICON[record.actor_type] ?? Cog
  const actor = record.actor_id ? `${record.actor_type} · ${shortId(record.actor_id, 12)}` : record.actor_type
  const hasPayload = !!record.payload && Object.keys(record.payload).length > 0
  const payloadLabel = t("audit.detail.payload")

  return (
    <li className="border-b border-line last:border-b-0">
      <div className="flex h-8 items-center gap-2 text-sm">
        <Icon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-fg" title={`${record.event_type} · ${actor}`}>
          {record.event_type}
          <span className="text-fg-muted"> · {actor}</span>
        </span>
        <span className="shrink-0 text-xs text-fg-muted" title={record.occurred_at}>
          {fmtAgo(record.occurred_at)}
        </span>
        {hasPayload && (
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            aria-expanded={open}
            aria-label={payloadLabel}
            title={payloadLabel}
            onClick={() => setOpen((v) => !v)}
          >
            <Code className={cn(open && "text-fg")} strokeWidth={1.5} />
          </Button>
        )}
      </div>
      {open && hasPayload && (
        <pre className="mb-2 mt-0 whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
          {`#${record.id} ${record.source}\n${JSON.stringify(record.payload ?? {}, null, 2)}`}
        </pre>
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
        description={query.error instanceof Error ? query.error.message : String(query.error)}
        onRetry={() => query.refetch()}
      />
    )
  }
  const records = query.data?.audit_records ?? []
  if (records.length === 0) {
    return (
      <EmptyState
        title={t("audit.resourceTimeline.empty.title", { defaultValue: "No audit events yet" })}
        description={t("audit.resourceTimeline.empty.description", {
          defaultValue: "This resource has not produced any audit records.",
        })}
        className="py-8"
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
