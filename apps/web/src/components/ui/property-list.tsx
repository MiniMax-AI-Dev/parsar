import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/**
 * Label / value grid used in detail rails and overview panes: a muted 12px
 * label column, 13px ink values, 28px rows. Values are never muted; pass
 * `mono` for identifiers, paths and timestamps.
 *
 * The label column is sized by its own longest label rather than by a number
 * someone picked: 84px was wide enough for Chinese and too narrow for
 * "Working directory", which wrapped inside a fixed 28px row and pushed its
 * value out of line with it. `max-content` grows to the labels actually
 * present — narrower in Chinese, wider in English — while the 5.25rem floor
 * keeps sections of a rail lined up with each other and a 12rem cap on the
 * label keeps a long one from eating the value column. (`fit-content()` is the
 * obvious tool here and cannot be used: it is not a legal max inside
 * `minmax()`, so the whole declaration is dropped and the grid silently
 * collapses to one column.) Label and value both truncate and both carry their
 * full text in a tooltip.
 */
export function PropertyList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl className={cn("m-0 grid grid-cols-[minmax(5.25rem,max-content)_minmax(0,1fr)] gap-x-3", className)}>
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
      <dt
        // A block, not a flex row: `text-overflow` only applies to block
        // containers, so a flex `dt` clips without ever drawing the ellipsis.
        // `leading-7` centres the one line in the 28px row instead. The cap is
        // what keeps `max-content` honest — a label long enough to eat the
        // value column truncates and keeps its `title`.
        className="h-7 max-w-48 truncate leading-7 text-xs text-fg-muted"
        title={typeof label === "string" ? label : undefined}
      >
        {label}
      </dt>
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
