import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/**
 * Label / value grid used in detail rails and overview panes: an 84px
 * muted 12px label column, 13px ink values, 28px rows. Values are
 * never muted; pass `mono` for identifiers, paths and timestamps.
 */
export function PropertyList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl className={cn("m-0 grid grid-cols-[84px_minmax(0,1fr)] gap-x-3", className)}>
      {children}
    </dl>
  )
}

export function Property({
  label,
  children,
  mono,
  className,
  title,
}: {
  label: ReactNode
  children: ReactNode
  mono?: boolean
  className?: string
  /** Overrides the tooltip; by default a string value becomes its own. */
  title?: string
}) {
  return (
    <>
      <dt className="flex h-7 items-center text-xs text-fg-muted">{label}</dt>
      <dd
        title={title ?? (typeof children === "string" ? children : undefined)}
        className={cn(
          "m-0 flex h-7 min-w-0 items-center gap-1.5 truncate text-sm text-fg",
          mono && "font-mono text-xs",
          className,
        )}
      >
        {children}
      </dd>
    </>
  )
}
