import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

// Shared tracks align independent property groups without letting labels crowd values.
export function PropertyList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <dl className={cn("m-0 grid grid-cols-[min(8rem,40%)_minmax(0,1fr)] gap-x-3", className)}>
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
  const plainText = typeof children === "string" || typeof children === "number"
  return (
    <>
      <dt
        className="min-h-7 min-w-0 py-1 text-xs leading-5 text-fg-muted [overflow-wrap:anywhere]"
        title={typeof label === "string" ? label : undefined}
      >
        {label}
      </dt>
      <dd
        title={title ?? (typeof children === "string" ? children : undefined)}
        className={cn(
          "m-0 min-w-0 text-sm text-fg",
          plainText
            ? "min-h-7 py-1 leading-5 [overflow-wrap:anywhere]"
            : "flex h-7 items-center gap-1.5 truncate",
          mono && "font-mono text-xs",
          className,
        )}
      >
        {children}
      </dd>
    </>
  )
}
