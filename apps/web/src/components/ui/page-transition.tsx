import * as React from "react"
import { cn } from "../../lib/utils"

export type PageLevel = "page" | "detail"

/**
 * The page entrance. Level-one views (opened from the sidebar) rise in
 * with `page-in`; level-two detail pages (opened from a list row) are
 * pushed in from the right with `detail-in`, so the two moves read as
 * "switch section" versus "drill down". Re-keyed by view (and by entity
 * for details) so navigation replays it. Off under reduced motion.
 */
export function PageTransition({
  viewKey,
  level = "page",
  className,
  children,
}: {
  viewKey: string
  level?: PageLevel
  className?: string
  children: React.ReactNode
}) {
  return (
    <div
      key={`${viewKey}:${level}`}
      className={cn("flex min-h-0 flex-1 flex-col", level === "detail" ? "animate-detail-in" : "animate-page-in", className)}
    >
      {children}
    </div>
  )
}
