import { useTranslation } from "react-i18next"
import { Bot, Cog, Globe, User as UserIcon } from "lucide-react"

import { Badge } from "../ui/badge"
import { EmptyState } from "../ui/empty-state"
import { ErrorState } from "../ui/error-state"
import { Skeleton } from "../ui/skeleton"
import { useAuditRecords } from "../../lib/api-governance"
import type { AuditActorType, AuditRecord, AuditSource } from "../../lib/api-types"
import { useRelativeTime } from "../../lib/relative-time"

type BadgeVariant = "primary" | "success" | "warning" | "destructive" | "neutral"

const SOURCE_BADGE: Record<AuditSource, BadgeVariant> = {
  identity: "neutral",
  admin: "primary",
  runtime: "success",
  approval: "warning",
  data: "neutral",
}

function shortId(s: string | undefined | null, n = 10): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}

function ActorIcon({ type }: { type: AuditActorType }) {
  if (type === "agent") return <Bot className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
  if (type === "user") return <UserIcon className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
  if (type === "external") return <Globe className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
  return <Cog className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
}

function PayloadPreview({ payload }: { payload?: Record<string, unknown> }) {
  if (!payload) return null
  const entries = Object.entries(payload).filter(([, v]) => v !== null && v !== undefined && v !== "")
  if (entries.length === 0) return null
  const preview = entries.slice(0, 3).map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`).join(" · ")
  return (
    <p className="mt-1 truncate font-mono text-[12px] text-slate-500" title={preview}>
      {preview}
    </p>
  )
}

function TimelineRow({ record, fmtAgo }: { record: AuditRecord; fmtAgo: (iso: string | null | undefined) => string }) {
  return (
    <li className="flex gap-3 border-b border-slate-100 px-1 py-2.5 last:border-b-0">
      <div className="flex w-32 shrink-0 flex-col gap-0.5 pt-0.5">
        <span className="text-[13px] text-slate-700" title={record.occurred_at}>
          {fmtAgo(record.occurred_at)}
        </span>
        <Badge variant={SOURCE_BADGE[record.source]} className="w-fit">
          {record.source}
        </Badge>
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[13px] font-medium text-slate-800" title={record.event_type}>
          {record.event_type}
        </p>
        <div className="mt-0.5 flex items-center gap-1.5 text-[12px] text-slate-500">
          <ActorIcon type={record.actor_type} />
          <span className="font-mono">
            {record.actor_type}
            {record.actor_id ? ` · ${shortId(record.actor_id, 12)}` : ""}
          </span>
        </div>
        <PayloadPreview payload={record.payload} />
      </div>
    </li>
  )
}

export interface ResourceAuditTimelineProps {
  /** Active project ID; null surfaces mock data on dev landing. */
  pid: string | null
  /** Resource discriminator the feed pins to (`agent_run`, `agent`, …). */
  targetType: string
  /** Required — without an ID we'd query the unfiltered project feed. */
  targetID: string
  /** Override the default 200-row cap. */
  limit?: number
}

export function ResourceAuditTimeline({
  pid,
  targetType,
  targetID,
  limit,
}: ResourceAuditTimelineProps) {
  const { t } = useTranslation("admin")
  const fmtAgo = useRelativeTime()
  const query = useAuditRecords(pid, {
    target_type: targetType,
    target_id: targetID,
    limit,
  })

  if (query.isLoading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
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
      />
    )
  }
  // Defensive re-sort: API returns newest-first, but a future cache
  // layer or mock could reorder.
  const sorted = [...records].sort((a, b) => b.occurred_at.localeCompare(a.occurred_at))
  return <ul className="divide-y divide-slate-100">{sorted.map((r) => <TimelineRow key={r.id} record={r} fmtAgo={fmtAgo} />)}</ul>
}
