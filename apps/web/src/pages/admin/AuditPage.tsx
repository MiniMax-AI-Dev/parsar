import { Fragment, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { ArrowUpRight, Check, Code, ListFilter, MessageSquare, Search, ShieldCheck } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ErrorState } from "../../components/ui/error-state"
import { Input } from "../../components/ui/input"
import { Kbd } from "../../components/ui/kbd"
import { InitialTile, Ledger, LedgerHeader, LedgerId, LedgerRow, col } from "../../components/ui/ledger"
import { Skeleton } from "../../components/ui/skeleton"
import { useAdminView } from "../../lib/admin-router"
import { ApiError } from "../../lib/api-client"
import { useAuditRecords } from "../../lib/api-governance"
import type { AuditRecord, AuditSource } from "../../lib/api-types"
import { useWorkspaceId } from "../../lib/workspace"
import { cn } from "../../lib/utils"

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

type SourceFilter = AuditSource | "all"

/**
 * Curated target_type options per source — only target_types that have
 * a server-side producer, so the dropdown doesn't advertise dead values.
 */
const SOURCE_TARGET_TYPES: Record<AuditSource, ReadonlyArray<string>> = {
  identity: [],
  admin: [
    "workspace",
    "workspace_member",
    "agent",
    "secret",
    "model_provider",
    "model",
  ],
  runtime: ["agent_run", "message"],
  approval: ["permission_request"],
  data: [],
}

/** time · source · actor · action · target · actions */
const LEDGER_COLUMNS = [col.id(140, 0.5), col.meta(64, 0.4), col.title(160, 1), col.text(160, 1.4), col.id(160, 1.2), col.actions(3)]

function shortId(s: string | undefined | null, n = 10): string {
  if (!s) return "—"
  return s.length <= n ? s : s.slice(0, n) + "…"
}

function fmtAbsTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, { hour12: false })
}

function payloadString(record: AuditRecord, key: string): string | null {
  const v = record.payload?.[key]
  return typeof v === "string" && v ? v : null
}

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export function AuditPage() {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const wsId = useWorkspaceId()
  const [source, setSource] = useState<SourceFilter>("all")
  const [targetType, setTargetType] = useState<string>("")
  const [keyword, setKeyword] = useState("")
  const [openRow, setOpenRow] = useState<number | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  // Backend filters server-side; client-side work is keyword search only.
  const query = useAuditRecords(wsId, {
    source: source === "all" ? undefined : source,
    target_type: targetType || undefined,
  })
  const rows = useMemo(() => query.data?.audit_records ?? [], [query.data])

  const err = query.error
  const isUnreachable = err instanceof ApiError && err.envelope.unreachable

  // Per-source counts only meaningful on "all" — when a source is active
  // the backend filter zeroes out the others.
  const counts = useMemo(() => {
    if (source !== "all") return null
    const c: Record<AuditSource, number> = {
      identity: 0,
      admin: 0,
      runtime: 0,
      approval: 0,
      data: 0,
    }
    for (const r of rows) c[r.source]++
    return c
  }, [rows, source])

  // Options come from the curated map (not the visible rows) — filtering
  // itself runs server-side, so the dropdown advertises *possible* values.
  const targetTypeOptions = useMemo<ReadonlyArray<string>>(() => {
    if (source === "all") {
      const set = new Set<string>()
      for (const s of SOURCES) {
        for (const tt of SOURCE_TARGET_TYPES[s]) set.add(tt)
      }
      return Array.from(set).sort()
    }
    return SOURCE_TARGET_TYPES[source]
  }, [source])

  function setSourceAndResetTarget(next: SourceFilter) {
    setSource(next)
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
    setSource("all")
    setTargetType("")
    setKeyword("")
  }

  // ⌘K focuses the search field, as on every ledger page.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [])

  const hasActiveFilters = source !== "all" || !!targetType || !!keyword.trim()
  const pageTitle = t("audit.page.title")
  const filterLabel = t("audit.filter.label")
  const targetTypeLabel = (tt: string) => t(`audit.targetType.${tt}`, { defaultValue: tt })

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="audit.page.title"
          action={
            <>
              <SettingsTabs active="audit" />
              <div className="relative w-72">
                <Search
                  className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                  strokeWidth={1.5}
                  aria-hidden="true"
                />
                <Input
                  ref={searchRef}
                  type="search"
                  placeholder={t("audit.search.placeholder")}
                  aria-label={t("audit.search.placeholder")}
                  className="pl-7 pr-11"
                  value={keyword}
                  onChange={(e) => setKeyword(e.target.value)}
                />
                <Kbd className="pointer-events-none absolute right-1.5 top-1/2 -translate-y-1/2">⌘K</Kbd>
              </div>
              <DropdownMenu.Root>
                <DropdownMenu.Trigger asChild>
                  <Button variant="outline" aria-haspopup="menu">
                    <ListFilter strokeWidth={1.5} aria-hidden="true" />
                    {filterLabel}
                    {source !== "all" && (
                      <span className="text-fg-muted">· {t(`audit.tabs.${source}`)}</span>
                    )}
                    {targetType && (
                      <span className="text-fg-muted">· {targetTypeLabel(targetType)}</span>
                    )}
                  </Button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content
                    align="end"
                    sideOffset={6}
                    className="app-shadow-floating z-50 min-w-[200px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
                  >
                    <DropdownMenu.Label className="px-2 pb-1 pt-1.5 text-xs text-fg-muted">
                      {t("audit.table.source")}
                    </DropdownMenu.Label>
                    <DropdownMenu.RadioGroup
                      value={source}
                      onValueChange={(v) => setSourceAndResetTarget(v as SourceFilter)}
                    >
                      <FilterItem value="all" label={t("audit.tabs.all")} count={counts ? rows.length : undefined} />
                      {SOURCES.map((s) => (
                        <FilterItem key={s} value={s} label={t(`audit.tabs.${s}`)} count={counts?.[s]} />
                      ))}
                    </DropdownMenu.RadioGroup>
                    {targetTypeOptions.length > 0 && (
                      <>
                        <DropdownMenu.Separator className="my-1 h-px bg-line" />
                        <DropdownMenu.Label className="px-2 pb-1 pt-1.5 text-xs text-fg-muted">
                          {t("audit.filters.targetType")}
                        </DropdownMenu.Label>
                        <DropdownMenu.RadioGroup value={targetType} onValueChange={setTargetType}>
                          <FilterItem value="" label={t("audit.filters.targetTypeAll")} />
                          {targetTypeOptions.map((tt) => (
                            <FilterItem key={tt} value={tt} label={targetTypeLabel(tt)} />
                          ))}
                        </DropdownMenu.RadioGroup>
                      </>
                    )}
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </>
          }
        />

        {!wsId ? (
          <ScopeRequiredState scope="workspace" resourceName={pageTitle} />
        ) : query.isLoading ? (
          <AuditLoadingSkeleton />
        ) : err ? (
          <div className="px-4 pt-4">
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
          </div>
        ) : filtered.length === 0 ? (
          <EmptyState
            icon={ShieldCheck}
            title={t("audit.empty.title")}
            description={hasActiveFilters ? t("audit.empty.filteredDescription") : t("audit.empty.description")}
            action={
              hasActiveFilters ? (
                <Button size="sm" variant="outline" onClick={clearFilters}>
                  {t("audit.empty.clearFilters")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <Ledger columns={LEDGER_COLUMNS} role="listbox" aria-label={pageTitle}>
            <LedgerHeader>
              <span>{t("audit.table.time")}</span>
              <span>{t("audit.table.source")}</span>
              <span>{t("audit.table.actor")}</span>
              <span>{t("audit.table.event")}</span>
              <span>{t("audit.table.target")}</span>
              <span />
            </LedgerHeader>
            <ul className="m-0 list-none p-0">
              {filtered.map((r) => (
                <Fragment key={r.id}>
                  <AuditRow
                    record={r}
                    open={openRow === r.id}
                    onToggle={() => setOpenRow((cur) => (cur === r.id ? null : r.id))}
                  />
                  {openRow === r.id && (
                    <li className="border-b border-line px-4 py-2">
                      <pre className="m-0 whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
                        {`#${r.id} ${r.event_type}\n${JSON.stringify(r.payload ?? {}, null, 2)}`}
                      </pre>
                    </li>
                  )}
                </Fragment>
              ))}
            </ul>
          </Ledger>
        )}

        {wsId && !query.isLoading && !err && rows.length > 0 && (
          <div className="flex h-10 shrink-0 items-center border-t border-line px-4 text-xs tabular-nums text-fg-muted">
            {tc("pagination.range", { from: filtered.length > 0 ? 1 : 0, to: filtered.length, total: rows.length })}
          </div>
        )}
      </div>
    </AdminLayout>
  )
}

function FilterItem({ value, label, count }: { value: string; label: string; count?: number }) {
  return (
    <DropdownMenu.RadioItem
      value={value}
      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
    >
      <span className="flex-1">{label}</span>
      {count !== undefined && <span className="text-xs tabular-nums text-fg-muted">{count}</span>}
      <DropdownMenu.ItemIndicator>
        <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
      </DropdownMenu.ItemIndicator>
    </DropdownMenu.RadioItem>
  )
}

/* ------------------------------------------------------------------ */
/*  Row                                                                */
/* ------------------------------------------------------------------ */

function AuditRow({ record, open, onToggle }: { record: AuditRecord; open: boolean; onToggle: () => void }) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()

  const isId = record.actor_type === "user" || record.actor_type === "agent"
  const actorLabel = isId && record.actor_id ? shortId(record.actor_id, 12) : record.actor_type
  const hasPayload = !!record.payload && Object.keys(record.payload).length > 0
  // Surface commonly-jumped ids as row actions so admins don't have to
  // read the JSON to navigate.
  const runId = record.target_type === "agent_run" && record.target_id
    ? record.target_id
    : payloadString(record, "agent_run_id")
  const convoId = payloadString(record, "conversation_id")
  const payloadLabel = t("audit.detail.payload")

  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onToggle()
    }
  }

  return (
    <LedgerRow selected={open} onClick={onToggle} onKeyDown={onKeyDown}>
      <span className="truncate font-mono text-xs tabular-nums text-fg-muted" title={record.occurred_at}>
        {fmtAbsTime(record.occurred_at)}
      </span>
      <span className="truncate text-xs text-fg-muted">{t(`audit.source.${record.source}`)}</span>
      <span className="flex min-w-0 items-center gap-1.5" title={record.actor_id ?? record.actor_type}>
        <InitialTile name={actorLabel} />
        <span className={cn("truncate", isId && "font-mono text-xs")}>{actorLabel}</span>
      </span>
      <span className="truncate" title={record.event_type}>{record.event_type}</span>
      {record.target_type ? (
        <LedgerId>{record.target_type} · {shortId(record.target_id, 10)}</LedgerId>
      ) : (
        <span className="text-xs text-fg-muted">—</span>
      )}
      <RowActions>
        {runId && (
          <ActionIconButton
            icon={ArrowUpRight}
            label={t("audit.detail.openRun")}
            onClick={() => navigate("runs", { id: runId })}
          />
        )}
        {convoId && (
          <ActionIconButton
            icon={MessageSquare}
            label={t("audit.detail.openConversation")}
            onClick={() => navigate("conversations", { id: convoId })}
          />
        )}
        {hasPayload && (
          <ActionIconButton
            icon={Code}
            label={payloadLabel}
            aria-expanded={open}
            className={cn(open && "text-fg")}
            onClick={onToggle}
          />
        )}
      </RowActions>
    </LedgerRow>
  )
}

/* ------------------------------------------------------------------ */
/*  Loading skeleton                                                   */
/* ------------------------------------------------------------------ */

function AuditLoadingSkeleton() {
  return (
    <div className="px-4 pt-3">
      <div className="mb-3 h-7 border-b border-line" />
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="flex h-9 items-center gap-3 border-b border-line">
          <Skeleton className="h-3 w-32" />
          <Skeleton className="h-3 w-12" />
          <Skeleton className="h-3 w-28" />
          <Skeleton className="h-3 flex-1" />
          <Skeleton className="h-3 w-24" />
        </div>
      ))}
    </div>
  )
}
