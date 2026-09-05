import * as React from "react"
import { cn } from "../../lib/utils"

/**
 * The one page entrance. Wraps a page's main column so every view enters
 * the same way (320ms spring rise + fade); re-keyed by the view name so
 * switching pages replays it. Off under reduced motion via the global rule.
 */
export function PageTransition({
  viewKey,
  className,
  children,
}: {
  viewKey: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div key={viewKey} className={cn("flex min-h-0 flex-1 flex-col animate-page-in", className)}>
      {children}
    </div>
  )
}
