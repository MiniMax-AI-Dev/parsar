import { Fragment, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  Bot,
  Cog,
  Globe,
  Search,
  ShieldCheck,
  User as UserIcon,
} from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Skeleton } from "../../components/ui/skeleton"
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "../../components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../components/ui/table"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAuditRecords } from "../../lib/api-governance"
import type { AuditRecord, AuditSource } from "../../lib/api-types"
import { useProjectId } from "../../lib/workspace"
import { useRelativeTime } from "../../lib/relative-time"

/* ------------------------------------------------------------------ */
/*  Constants                                                          */
/* ------------------------------------------------------------------ */

const SOURCES: ReadonlyArray<AuditSource> = [
  "identity",
  "admin",
  "runtime",
  "approval",
  "data",
] as const

type SourceTab = AuditSource | "all"

/**
 * Curated target_type options per source — only target_types that have
 * a server-side producer, so the dropdown doesn't advertise dead values.
 */
const SOURCE_TARGET_TYPES: Record<AuditSource, ReadonlyArray<string>> = {
  identity: [],
  admin: [
    "workspace",
    "workspace_member",
    "project",
    "project_member",
    "project_agent",
    "secret",
    "model_provider",
    "model",
  ],
  runtime: ["agent_run", "message"],
  approval: ["permission_request"],
  data: [],
}

/* ------------------------------------------------------------------ */
/*  Source badge                                                       */
/* ------------------------------------------------------------------ */

type BadgeVariant = "primary" | "success" | "warning" | "destructive" | "neutral"

const SOURCE_BADGE: Record<AuditSource, BadgeVariant> = {
  identity: "neutral",
  admin: "primary",
  runtime: "success",
  approval: "warning",
  data: "neutral",
}

function SourceBadge({ source }: { source: AuditSource }) {
  const { t } = useTranslation("admin")
  return (
    <Badge variant={SOURCE_BADGE[source]}>
      {t(`audit.source.${source}`)}
    </Badge>
  )
}

/* ------------------------------------------------------------------ */
/*  Actor display                                                      */
/* ------------------------------------------------------------------ */

function shortId(s: string | undefined | null, n = 10): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}

function ActorCell({ row }: { row: AuditRecord }) {
  if (row.actor_type === "agent") {
    return (
      <span className="inline-flex items-center gap-1.5 text-[13px] text-slate-700">
        <Bot className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
        <span className="font-mono text-[12px] text-slate-500">
          {shortId(row.actor_id, 10)}
        </span>
      </span>
    )
  }
  if (row.actor_type === "user") {
    return (
      <span className="inline-flex items-center gap-1.5 text-[13px] text-slate-700">
        <UserIcon className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
        <span className="font-mono text-[12px] text-slate-500">
          {shortId(row.actor_id, 10)}
        </span>
      </span>
    )
  }
  if (row.actor_type === "external") {
    return (
      <span className="inline-flex items-center gap-1.5 text-[13px] text-slate-600">
        <Globe className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
        external
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[13px] text-slate-600">
      <Cog className="h-3 w-3 text-slate-400" strokeWidth={1.75} />
      system
    </span>
  )
}

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function AuditPage() {
  const { t } = useTranslation("admin")
  const pid = useProjectId()
  const [tab, setTab] = useState<SourceTab>("all")
  const [targetType, setTargetType] = useState<string>("")
  const [keyword, setKeyword] = useState("")
  const [openRow, setOpenRow] = useState<number | null>(null)
  const { navigate } = useAdminView()
  const fmtAgo = useRelativeTime()

  // Backend filters server-side; client-side work is keyword search only.
  const query = useAuditRecords(pid, {
    source: tab === "all" ? undefined : tab,
    target_type: targetType || undefined,
  })
  const rows = useMemo(() => query.data?.audit_records ?? [], [query.data])

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  // Per-tab counts only meaningful on "all" — when a tab is active the
  // backend filter zeroes out the others.
  const counts = useMemo(() => {
    if (tab !== "all") return null
    const c: Record<AuditSource, number> = {
      identity: 0,
      admin: 0,
      runtime: 0,
      approval: 0,
      data: 0,
    }
    for (const r of rows) c[r.source]++
    return c
  }, [rows, tab])

  // Options come from the curated map (not the visible rows) — filtering
  // itself runs server-side, so the dropdown advertises *possible* values.
  const targetTypeOptions = useMemo<ReadonlyArray<string>>(() => {
    if (tab === "all") {
      const set = new Set<string>()
      for (const s of SOURCES) {
        for (const tt of SOURCE_TARGET_TYPES[s]) set.add(tt)
      }
      return Array.from(set).sort()
    }
    return SOURCE_TARGET_TYPES[tab]
  }, [tab])

  function setTabAndResetTarget(next: SourceTab) {
    setTab(next)
    setTargetType("")
  }

  // Client-side keyword match: event_type / actor_id / target_id /
  // target_type — what an admin pastes when chasing an incident.
  const filtered = useMemo(() => {
    if (!keyword.trim()) return rows
    const q = keyword.trim().toLowerCase()
    return rows.filter((r) =>
      r.event_type.toLowerCase().includes(q) ||
      (r.actor_id ?? "").toLowerCase().includes(q) ||
      (r.target_id ?? "").toLowerCase().includes(q) ||
      (r.target_type ?? "").toLowerCase().includes(q)
    )
  }, [rows, keyword])

  function clearFilters() {
    setTab("all")
    setTargetType("")
    setKeyword("")
  }

  const hasActiveFilters = tab !== "all" || !!targetType || !!keyword.trim()

  return (
    <AdminLayout activeMenu="audit">
      <PageHeader
        title={t("audit.page.title")}
        description={t("audit.page.description")}
      />
      {!pid ? (
        <ScopeRequiredState scope="project" resourceName={t("audit.page.title")} />
      ) : query.isLoading ? (
        <AuditLoadingSkeleton />
      ) : err ? (
        <ErrorState
          title={isUnreachable ? t("audit.loadError.unreachable.title") : t("audit.loadError.title")}
          description={
            isUnreachable
              ? t("audit.loadError.unreachable.description")
              : err instanceof Error
                ? err.message
                : t("audit.loadError.description")
          }
          hint={isUnreachable ? t("audit.loadError.unreachable.hint") : t("audit.loadError.hint")}
          onRetry={() => void query.refetch()}
        />
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <Tabs value={tab} onValueChange={(v) => setTabAndResetTarget(v as SourceTab)}>
              <TabsList>
                <TabsTrigger value="all">{t("audit.tabs.all")}</TabsTrigger>
                {SOURCES.map((s) => (
                  <TabsTrigger key={s} value={s}>
                    {t(`audit.tabs.${s}`)}
                    {counts && counts[s] > 0 && (
                      <span className="ml-1.5 text-[12px] text-slate-400 tabular-nums">
                        {counts[s]}
                      </span>
                    )}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>

            <div className="flex items-center gap-2">
              <select
                value={targetType}
                onChange={(e) => setTargetType(e.target.value)}
                aria-label={t("audit.filters.targetType")}
                disabled={targetTypeOptions.length === 0}
                className="rounded-md border border-slate-200 bg-white px-2.5 py-1.5 text-[13px] text-slate-700 focus:border-slate-400 focus:outline-none focus:ring-1 focus:ring-slate-200 disabled:opacity-50"
              >
                <option value="">
                  {targetTypeOptions.length === 0
                    ? t("audit.filters.targetTypeNone")
                    : t("audit.filters.targetTypeAll")}
                </option>
                {targetTypeOptions.map((tt) => (
                  <option key={tt} value={tt}>
                    {t(`audit.targetType.${tt}`, { defaultValue: tt })}
                  </option>
                ))}
              </select>

              <div className="relative w-64">
                <Search
                  className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-400"
                  strokeWidth={1.75}
                />
                <Input
                  placeholder={t("audit.search.placeholder")}
                  className="pl-8 text-xs"
                  value={keyword}
                  onChange={(e) => setKeyword(e.target.value)}
                />
              </div>
            </div>
          </div>

          {filtered.length === 0 ? (
            <EmptyState
              icon={ShieldCheck}
              title={t("audit.empty.title")}
              description={
                hasActiveFilters
                  ? t("audit.empty.filteredDescription")
                  : t("audit.empty.description")
              }
              action={
                hasActiveFilters ? (
                  <Button size="sm" variant="outline" onClick={clearFilters}>
                    {t("audit.empty.clearFilters")}
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <div className="overflow-hidden rounded-lg border border-slate-200 bg-white">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-40">{t("audit.table.time")}</TableHead>
                    <TableHead className="w-24">{t("audit.table.source")}</TableHead>
                    <TableHead className="w-44">{t("audit.table.actor")}</TableHead>
                    <TableHead>{t("audit.table.event")}</TableHead>
                    <TableHead>{t("audit.table.target")}</TableHead>
                    <TableHead className="text-right pr-4 w-28">{t("audit.table.payload")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((r) => {
                    const isOpen = openRow === r.id
                    const targetClickable =
                      r.target_type === "agent_run" && !!r.target_id
                    const payloadEntries = r.payload ? Object.keys(r.payload).length : 0
                    return (
                      <Fragment key={r.id}>
                        <TableRow
                          className="cursor-pointer"
                          onClick={() => setOpenRow(isOpen ? null : r.id)}
                        >
                          <TableCell>
                            <div className="flex flex-col">
                              <span className="text-[13px] text-slate-700">
                                {fmtAgo(r.occurred_at)}
                              </span>
                              <span className="font-mono text-[11px] text-slate-400 tabular-nums">
                                {fmtAbsTime(r.occurred_at)}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell><SourceBadge source={r.source} /></TableCell>
                          <TableCell><ActorCell row={r} /></TableCell>
                          <TableCell>
                            <code className="text-[13px] text-slate-800">{r.event_type}</code>
                          </TableCell>
                          <TableCell>
                            {targetClickable ? (
                              <button
                                className="font-mono text-[12px] text-slate-700 hover:underline"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  navigate("runs", { id: r.target_id! })
                                }}
                              >
                                {r.target_type} · {shortId(r.target_id, 10)}
                              </button>
                            ) : r.target_type ? (
                              <span className="font-mono text-[12px] text-slate-600">
                                {r.target_type} · {shortId(r.target_id, 10)}
                              </span>
                            ) : (
                              <span className="text-[12px] text-slate-400">—</span>
                            )}
                          </TableCell>
                          <TableCell className="text-right pr-4">
                            {payloadEntries > 0 ? (
                              <Badge variant="neutral">
                                {t("audit.table.payloadFields", { count: payloadEntries })}
                              </Badge>
                            ) : (
                              <span className="text-[12px] text-slate-400">—</span>
                            )}
                          </TableCell>
                        </TableRow>
                        {isOpen && (
                          <TableRow key={`${r.id}-payload`}>
                            <TableCell colSpan={6} className="bg-slate-50/60">
                              <PayloadView record={r} />
                            </TableCell>
                          </TableRow>
                        )}
                      </Fragment>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}

          {rows.length > 0 && (
            <p className="text-[12px] text-slate-400">
              {t("audit.footer.shownCount", {
                shown: filtered.length,
                total: rows.length,
              })}
            </p>
          )}
        </div>
      )}
    </AdminLayout>
  )
}

/* ------------------------------------------------------------------ */
/*  Expanded payload row                                               */
/* ------------------------------------------------------------------ */

function PayloadView({ record }: { record: AuditRecord }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()

  // Surface commonly-jumped ids as quick links so admins don't have to
  // read the JSON to navigate.
  const convoId =
    typeof record.payload?.conversation_id === "string"
      ? (record.payload.conversation_id as string)
      : null
  const runId =
    typeof record.payload?.agent_run_id === "string"
      ? (record.payload.agent_run_id as string)
      : null

  return (
    <div className="space-y-3 py-2">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <Field
          label={t("audit.detail.recordId")}
          value={<span className="font-mono text-[12px]">{record.id}</span>}
        />
        <Field
          label={t("audit.detail.actorType")}
          value={<span className="font-mono text-[12px]">{record.actor_type}</span>}
        />
        <Field
          label={t("audit.detail.target")}
          value={
            <span className="font-mono text-[12px] break-all">
              {record.target_type ?? "—"} · {record.target_id ?? "—"}
            </span>
          }
        />
      </div>

      {(convoId || runId) && (
        <div className="flex flex-wrap items-center gap-2">
          {runId && (
            <Button size="sm" variant="outline" onClick={() => navigate("runs", { id: runId })}>
              {t("audit.detail.openRun")}
            </Button>
          )}
          {convoId && (
            <Button size="sm" variant="outline" onClick={() => navigate("conversations", { id: convoId })}>
              {t("audit.detail.openConversation")}
            </Button>
          )}
        </div>
      )}

      <div>
        <p className="mb-1 text-[12px] uppercase tracking-wider text-slate-400">
          {t("audit.detail.payload")}
        </p>
        <pre className="whitespace-pre-wrap rounded-md bg-white p-3 font-mono text-[12px] text-slate-700 ring-1 ring-slate-200">
{JSON.stringify(record.payload ?? {}, null, 2)}
        </pre>
      </div>
    </div>
  )
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="mb-0.5 text-[12px] uppercase tracking-wider text-slate-400">{label}</dt>
      <dd className="text-[13px] text-slate-800">{value}</dd>
    </div>
  )
}

function fmtAbsTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, { hour12: false })
}

/* ------------------------------------------------------------------ */
/*  Loading skeleton                                                   */
/* ------------------------------------------------------------------ */

function AuditLoadingSkeleton() {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Skeleton className="h-8 w-96" />
        <Skeleton className="h-8 w-64" />
      </div>
      <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    </div>
  )
}
