import { Globe, Search, Send, X } from "lucide-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import {
  useDiscoverableWorkspaces,
  useWithdrawJoinRequest,
} from "../../lib/api-workspaces"
import type { DiscoverableWorkspace } from "../../lib/api-types"
import { ActionIconButton, RowActions } from "../ui/action-button"
import { Badge } from "../ui/badge"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../ui/dialog"
import { EmptyState } from "../ui/empty-state"
import { Input } from "../ui/input"
import {
  InitialTile,
  Ledger,
  LedgerHeader,
  LedgerId,
  LedgerNum,
  LedgerRow,
} from "../ui/ledger"
import { OffsetPagination } from "../ui/offset-pagination"
import { Skeleton } from "../ui/skeleton"

interface DiscoverWorkspacesDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Parent opens JoinRequestDialog (reason input); nesting two
   *  Radix dialogs would fight over focus trap. */
  onSelectToJoin: (ws: DiscoverableWorkspace) => void
}

const PAGE_SIZE = 20

/** workspace (tile · name · slug · pending badge) · members · one action */
const LEDGER_COLUMNS = "minmax(0,1fr) 64px 28px"

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
  const searchLabel = t("workspaceSwitcher.discoverSearchPlaceholder")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        aria-describedby={undefined}
        className="flex max-h-[80vh] max-w-[640px] flex-col"
      >
        <DialogHeader>
          <DialogTitle>{t("workspaceSwitcher.discoverDialogTitle")}</DialogTitle>
        </DialogHeader>

        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
            strokeWidth={1.5}
            aria-hidden="true"
          />
          <Input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={searchLabel}
            aria-label={searchLabel}
            className="pl-7"
            autoFocus
          />
        </div>

        <div className="-mx-4 -mb-4 flex min-h-0 flex-1 flex-col overflow-hidden rounded-b-lg border-t border-line">
          <Ledger columns={LEDGER_COLUMNS}>
            <LedgerHeader>
              <span>{t("workspaceCrud.fields.name")}</span>
              <span className="text-right">{t("workspaceSwitcher.membersColumn")}</span>
              <span />
            </LedgerHeader>
            {query.isLoading ? (
              <div className="px-4">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="flex h-9 items-center border-b border-line">
                    <Skeleton className="h-3.5 w-48" />
                  </div>
                ))}
              </div>
            ) : items.length === 0 ? (
              <EmptyState
                icon={Globe}
                title={
                  debouncedQ
                    ? t("workspaceSwitcher.discoverNoMatch", { q: debouncedQ })
                    : t("workspaceSwitcher.discoverEmpty")
                }
              />
            ) : (
              <ul className="m-0 list-none p-0">
                {items.map((ws) => (
                  <LedgerRow key={ws.id}>
                    <span className="flex min-w-0 items-center gap-2">
                      <InitialTile name={ws.name} />
                      <span className="truncate font-medium">{ws.name}</span>
                      <LedgerId>{ws.slug}</LedgerId>
                      {ws.has_pending_request && (
                        <Badge dot title={t("workspaceSwitcher.pendingRequestTitle")}>
                          {t("workspaceSwitcher.pendingRequestBadge")}
                        </Badge>
                      )}
                    </span>
                    <LedgerNum>{ws.member_count}</LedgerNum>
                    <RowActions>
                      {ws.has_pending_request ? (
                        <ActionIconButton
                          icon={X}
                          label={t("workspaceSwitcher.withdrawRequestAction")}
                          busy={withdrawMut.isPending && withdrawMut.variables?.wsId === ws.id}
                          onClick={() => withdrawMut.mutate({ wsId: ws.id })}
                        />
                      ) : (
                        <ActionIconButton
                          icon={Send}
                          label={t("workspaceSwitcher.requestJoinAction")}
                          onClick={() => onSelectToJoin(ws)}
                        />
                      )}
                    </RowActions>
                  </LedgerRow>
                ))}
              </ul>
            )}
          </Ledger>
          <OffsetPagination
            offset={page * PAGE_SIZE}
            limit={PAGE_SIZE}
            total={total}
            onPrevious={() => setPage((p) => Math.max(0, p - 1))}
            onNext={() => setPage((p) => p + 1)}
          />
        </div>
      </DialogContent>
    </Dialog>
  )
}
