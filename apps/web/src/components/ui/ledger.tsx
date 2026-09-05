import * as React from "react"
import { ChevronDown } from "lucide-react"
import { cn } from "../../lib/utils"

/**
 * The ledger: the list idiom of the whole console. A sticky 28px muted
 * column header, optional collapsible status groups, and 36px rows on a
 * shared CSS grid. Callers pass the grid template once via `columns`
 * (e.g. "14px 104px minmax(0,1fr) 140px 72px") so header and rows align.
 */

const LedgerContext = React.createContext<string>("minmax(0,1fr)")

export function Ledger({
  columns,
  className,
  children,
  ...props
}: { columns: string } & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <LedgerContext.Provider value={columns}>
      <div className={cn("min-h-0 flex-1 overflow-y-auto", className)} {...props}>
        {children}
      </div>
    </LedgerContext.Provider>
  )
}

function useColumns() {
  return React.useContext(LedgerContext)
}

export function LedgerHeader({ children, className }: { children: React.ReactNode; className?: string }) {
  const columns = useColumns()
  return (
    <div
      aria-hidden="true"
      style={{ gridTemplateColumns: columns }}
      className={cn(
        "sticky top-0 z-[1] grid h-7 items-center gap-x-2.5 border-b border-line bg-surface px-4 text-xs text-fg-muted",
        className,
      )}
    >
      {children}
    </div>
  )
}

export function LedgerGroup({
  label,
  count,
  defaultOpen = true,
  children,
}: {
  label: React.ReactNode
  count?: number
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const [open, setOpen] = React.useState(defaultOpen)
  return (
    <section>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex h-7 w-full items-center gap-1.5 border-b border-line px-3.5 text-left text-xs text-fg-muted hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"
      >
        <ChevronDown
          className={cn(
            "h-3.5 w-3.5 text-fg-muted transition-transform duration-200 ease-spring",
            !open && "-rotate-90",
          )}
          strokeWidth={1.5}
          aria-hidden="true"
        />
        <span className="font-medium text-fg">{label}</span>
        {count !== undefined && <span className="tabular-nums">{count}</span>}
      </button>
      {open && <ul className="m-0 list-none p-0">{children}</ul>}
    </section>
  )
}

export const LedgerRow = React.forwardRef<
  HTMLLIElement,
  React.LiHTMLAttributes<HTMLLIElement> & { selected?: boolean }
>(({ selected, className, children, ...props }, ref) => {
  const columns = useColumns()
  return (
    <li
      ref={ref}
      role="option"
      aria-selected={selected}
      tabIndex={0}
      style={{ gridTemplateColumns: columns }}
      className={cn(
        "group relative grid h-9 cursor-default items-center gap-x-2.5 border-b border-line px-4 text-sm text-fg outline-none transition-colors duration-150 ease-settle hover:app-hover focus-visible:app-hover",
        selected && "app-selected before:absolute before:bottom-0 before:left-0 before:top-0 before:w-0.5 before:bg-accent before:content-['']",
        className,
      )}
      {...props}
    >
      {children}
    </li>
  )
})
LedgerRow.displayName = "LedgerRow"

/** Right-aligned tabular mono cell for durations, costs, counts. */
export function LedgerNum({ children, muted, className }: { children: React.ReactNode; muted?: boolean; className?: string }) {
  return (
    <span className={cn("truncate text-right font-mono text-xs tabular-nums", muted ? "text-fg-muted" : "text-fg", className)}>
      {children}
    </span>
  )
}

/** Muted mono identifier cell. */
export function LedgerId({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("truncate font-mono text-xs text-fg-muted", className)}>{children}</span>
}

/** 18px initials tile used for agents and people inside rows. */
export function InitialTile({ name, className }: { name: string; className?: string }) {
  const initial = name.trim().slice(0, 1).toUpperCase() || "?"
  return (
    <span
      aria-hidden="true"
      className={cn("app-tile inline-flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded text-2xs font-medium text-fg", className)}
    >
      {initial}
    </span>
  )
}
