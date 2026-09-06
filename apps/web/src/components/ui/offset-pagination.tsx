import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { Button } from "./button"

interface OffsetPaginationProps {
  offset: number
  limit: number
  total: number
  /** Defaults to the shared common:pagination.* strings; pages should not override. */
  rangeLabel?: (range: { from: number; to: number; total: number }) => string
  previousLabel?: string
  nextLabel?: string
  onPrevious: () => void
  onNext: () => void
  className?: string
}

/**
 * The 40px list footer: range on the left, page buttons beside it.
 * Boundary buttons disable (not hide) so the layout never shifts.
 */
export function OffsetPagination({
  offset,
  limit,
  total,
  rangeLabel,
  previousLabel,
  nextLabel,
  onPrevious,
  onNext,
  className,
}: OffsetPaginationProps) {
  const { t } = useTranslation("common")
  if (total === 0) return null

  const range = rangeLabel ?? ((r: { from: number; to: number; total: number }) => t("pagination.range", r))
  const prev = previousLabel ?? t("pagination.prev")
  const next = nextLabel ?? t("pagination.next")
  const from = offset + 1
  const to = Math.min(offset + limit, total)
  const onFirstPage = offset === 0
  const onLastPage = offset + limit >= total

  return (
    <div
      className={cn(
        "flex h-10 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-fg-muted",
        className,
      )}
    >
      <span className="tabular-nums">{range({ from, to, total })}</span>
      <div className="flex items-center gap-1">
        <Button size="sm" variant="outline" onClick={onPrevious} disabled={onFirstPage}>
          {prev}
        </Button>
        <Button size="sm" variant="outline" onClick={onNext} disabled={onLastPage}>
          {next}
        </Button>
      </div>
    </div>
  )
}
