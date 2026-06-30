import * as Dialog from "@radix-ui/react-dialog"
import { Clock, Globe, Search, Send, X } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  useDiscoverableWorkspaces,
  useWithdrawJoinRequest,
} from "../../lib/api-workspaces"
import type { DiscoverableWorkspace } from "../../lib/api-types"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { Input } from "../ui/input"
import { Skeleton } from "../ui/skeleton"

interface DiscoverWorkspacesDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Parent opens JoinRequestDialog (reason input); nesting two
   *  Radix dialogs would fight over focus trap. */
  onSelectToJoin: (ws: DiscoverableWorkspace) => void
}

const PAGE_SIZE = 20

export function DiscoverWorkspacesDialog({
  open,
  onOpenChange,
  onSelectToJoin,
}: DiscoverWorkspacesDialogProps) {
  const { t } = useTranslation("common")
  const [searchInput, setSearchInput] = useState("")
  const [debouncedQ, setDebouncedQ] = useState("")
  const [page, setPage] = useState(0)

  // Reset local state on close so reopen starts clean.
  useEffect(() => {
    if (!open) {
      setSearchInput("")
      setDebouncedQ("")
      setPage(0)
    }
  }, [open])

  // 300ms debounce + reset page (stale offset is meaningless after query change).
  useEffect(() => {
    const id = window.setTimeout(() => {
      setDebouncedQ(searchInput.trim())
      setPage(0)
    }, 300)
    return () => window.clearTimeout(id)
  }, [searchInput])

  const query = useDiscoverableWorkspaces({
    q: debouncedQ,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    enabled: open,
  })
  const withdrawMut = useWithdrawJoinRequest()

  const items = query.data?.workspaces ?? []
  const total = query.data?.total ?? 0
  const startIndex = page * PAGE_SIZE + (items.length > 0 ? 1 : 0)
  const endIndex = page * PAGE_SIZE + items.length
  const hasPrev = page > 0
  const hasNext = endIndex < total

  const rangeLabel = useMemo(() => {
    if (total === 0) return ""
    return t("workspaceSwitcher.discoverRange", {
      start: startIndex,
      end: endIndex,
      total,
    })
  }, [startIndex, endIndex, total, t])

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/40 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 flex w-[min(720px,92vw)] max-h-[80vh] -translate-x-1/2 -translate-y-1/2 flex-col gap-4 rounded-lg border border-line bg-surface p-5 shadow-xl outline-none data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0">
          <div className="flex items-start justify-between gap-4">
            <div className="flex flex-col gap-1">
              <Dialog.Title className="text-base font-semibold text-fg">
                {t("workspaceSwitcher.discoverDialogTitle")}
              </Dialog.Title>
              <Dialog.Description className="text-sm text-fg-subtle">
                {t("workspaceSwitcher.discoverDialogDescription")}
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <button
                type="button"
                className="rounded p-1 text-fg-faint hover:bg-surface-muted hover:text-fg-muted"
                aria-label={t("actions.cancel")}
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint"
              strokeWidth={1.75}
            />
            <Input
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder={t("workspaceSwitcher.discoverSearchPlaceholder")}
              className="pl-8"
              autoFocus
            />
          </div>

          <div className="flex-1 overflow-y-auto rounded-md border border-line-muted">
            {query.isLoading ? (
              <div className="space-y-2 p-3">
                <Skeleton className="h-12 w-full" />
                <Skeleton className="h-12 w-full" />
                <Skeleton className="h-12 w-full" />
              </div>
            ) : items.length === 0 ? (
              <div className="p-6">
                <EmptyState
                  icon={Globe}
                  title={
                    debouncedQ
                      ? t("workspaceSwitcher.discoverNoMatch", {
                          q: debouncedQ,
                        })
                      : t("workspaceSwitcher.discoverEmpty")
                  }
                />
              </div>
            ) : (
              <ul className="divide-y divide-slate-100">
                {items.map((ws) => (
                  <li
                    key={ws.id}
                    className="flex items-center gap-3 px-3 py-2.5 hover:bg-surface-subtle"
                  >
                    <div className="flex flex-1 flex-col min-w-0">
                      <span className="truncate text-sm text-fg">
                        {ws.name}
                      </span>
                      <span className="truncate font-mono text-xs text-fg-faint">
                        {ws.slug}
                      </span>
                    </div>
                    <span className="text-xs text-fg-subtle">
                      {t("workspaceSwitcher.memberCount", {
                        count: ws.member_count,
                      })}
                    </span>
                    {ws.has_pending_request ? (
                      <div className="flex items-center gap-1.5">
                        <span
                          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-warning"
                          title={t("workspaceSwitcher.pendingRequestTitle")}
                        >
                          <Clock className="h-3 w-3" strokeWidth={1.75} />
                          {t("workspaceSwitcher.pendingRequestBadge")}
                        </span>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={withdrawMut.isPending}
                          onClick={() =>
                            withdrawMut.mutate({ wsId: ws.id })
                          }
                        >
                          <X className="h-3 w-3" strokeWidth={1.75} />
                          {t("workspaceSwitcher.withdrawRequestAction")}
                        </Button>
                      </div>
                    ) : (
                      <Button
                        type="button"
                        size="sm"
                        onClick={() => onSelectToJoin(ws)}
                      >
                        <Send className="h-3 w-3" strokeWidth={1.75} />
                        {t("workspaceSwitcher.requestJoinAction")}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="flex items-center justify-between text-sm text-fg-subtle">
            <span>{rangeLabel}</span>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!hasPrev || query.isFetching}
                onClick={() => setPage((p) => Math.max(0, p - 1))}
              >
                {t("workspaceSwitcher.paginationPrev")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!hasNext || query.isFetching}
                onClick={() => setPage((p) => p + 1)}
              >
                {t("workspaceSwitcher.paginationNext")}
              </Button>
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
