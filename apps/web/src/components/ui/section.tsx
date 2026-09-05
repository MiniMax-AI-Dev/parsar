import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/**
 * Section of a full-page detail or settings page: a 13px/500 head with an
 * optional muted count and one right-aligned action, content below.
 * Sections are separated by spacing only; there is no card. (Inside a
 * DetailRail use RailSection, whose head is 12px.)
 */
export function PageSection({
  title,
  meta,
  action,
  children,
  className,
}: {
  title: ReactNode
  meta?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("mt-6 first:mt-0", className)}>
      <div className="mb-2 flex h-7 items-center justify-between gap-2">
        <h2 className="flex items-baseline gap-2 text-sm font-medium text-fg">
          <span>{title}</span>
          {meta !== undefined && <span className="text-xs font-normal tabular-nums text-fg-muted">{meta}</span>}
        </h2>
        {action}
      </div>
      {children}
    </section>
  )
}
