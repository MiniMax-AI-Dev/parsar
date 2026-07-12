import { ChevronLeft, ChevronRight } from "lucide-react"

import { cn } from "../../lib/utils"
import { Button } from "./button"

interface OffsetPaginationProps {
  offset: number
  limit: number
  total: number
  rangeLabel: (range: { from: number; to: number; total: number }) => string
  previousLabel: string
  nextLabel: string
  onPrevious: () => void
  onNext: () => void
  className?: string
}

// Disable boundary buttons (vs. hiding) so layout stays stable.
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
  if (total === 0) return null

  const from = offset + 1
  const to = Math.min(offset + limit, total)
  const onFirstPage = offset === 0
  const onLastPage = offset + limit >= total

  return (
    <div className={cn("flex items-center justify-between gap-3 px-1", className)}>
      <span className="tabular-nums">{rangeLabel({ from, to, total })}</span>
      <div className="flex items-center gap-2">
        <Button size="sm" variant="outline" onClick={onPrevious} disabled={onFirstPage}>
          <ChevronLeft className="h-3.5 w-3.5" />
          {previousLabel}
        </Button>
        <Button size="sm" variant="outline" onClick={onNext} disabled={onLastPage}>
          {nextLabel}
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  )
}
