/* eslint-disable react-refresh/only-export-components */
import * as React from "react"
import { ChevronDown } from "lucide-react"
import { cn } from "../../lib/utils"

/**
 * The ledger: the list idiom of the whole console. A sticky 28px muted
 * column header, optional collapsible status groups, and 36px rows on a
 * shared CSS grid. Callers pass the grid template once via `columns`
 * (e.g. "14px 104px minmax(0,1fr) 140px 72px") so header and rows align.
 */

/* ------------------------------------------------------------------
 * Column model. A ledger declares WHAT each column holds; the primitive
 * decides the grid. Every content column is `minmax(min, weight fr)`, so
 * each keeps a floor wide enough for its content and the row always
 * fills the page, with the spare width shared in proportion to the
 * weights (title 2 · text 1 · meta 0.8 · id 0.7 · age 0.6 · num 0.5).
 * Icons, checkboxes and action clusters stay fixed. Legacy string
 * templates are adapted with the same rule (fixed px ≥ 56 become
 * minmax(px, px/240 fr), fluid tracks keep twice any fixed weight) so
 * older pages fill the page too.
 * ---------------------------------------------------------------- */

export type LedgerColumn =
  | { kind: "icon"; width?: number }
  | { kind: "check" }
  | { kind: "tile" }
  | { kind: "title"; min?: number; weight?: number }
  | { kind: "text"; min?: number; weight?: number }
  | { kind: "meta"; min?: number; weight?: number }
  | { kind: "id"; min?: number; weight?: number }
  | { kind: "num"; min?: number; weight?: number }
  | { kind: "age"; min?: number; weight?: number }
  | { kind: "actions"; count: number }
  | { kind: "fixed"; width: number }

const DEFAULTS: Record<string, { min: number; weight: number }> = {
  title: { min: 200, weight: 2 },
  text: { min: 120, weight: 1 },
  meta: { min: 104, weight: 0.8 },
  id: { min: 112, weight: 0.7 },
  age: { min: 80, weight: 0.6 },
  num: { min: 64, weight: 0.5 },
}

/** Column helpers so pages read as intent: `[col.icon(), col.id(), col.title(), col.num()]`. */
export const col = {
  icon: (width = 14): LedgerColumn => ({ kind: "icon", width }),
  check: (): LedgerColumn => ({ kind: "check" }),
  tile: (): LedgerColumn => ({ kind: "tile" }),
  title: (min?: number, weight?: number): LedgerColumn => ({ kind: "title", min, weight }),
  text: (min?: number, weight?: number): LedgerColumn => ({ kind: "text", min, weight }),
  meta: (min?: number, weight?: number): LedgerColumn => ({ kind: "meta", min, weight }),
  id: (min?: number, weight?: number): LedgerColumn => ({ kind: "id", min, weight }),
  num: (min?: number, weight?: number): LedgerColumn => ({ kind: "num", min, weight }),
  age: (min?: number, weight?: number): LedgerColumn => ({ kind: "age", min, weight }),
  actions: (count = 1): LedgerColumn => ({ kind: "actions", count }),
  fixed: (width: number): LedgerColumn => ({ kind: "fixed", width }),
}

function trackFor(c: LedgerColumn): string {
  switch (c.kind) {
    case "icon":
      return `${c.width ?? 14}px`
    case "check":
      return "16px"
    case "tile":
      return "18px"
    case "actions":
      // No track: RowActions overlays the row's right end on hover, so
      // every ledger's last content column ends at the same edge.
      return "0px"
    case "fixed":
      return `${c.width}px`
    default: {
      const d = DEFAULTS[c.kind]
      const min = c.min ?? d.min
      const weight = c.weight ?? d.weight
      return `minmax(${min}px, ${weight}fr)`
    }
  }
}

/** Build the grid-template-columns string from a column spec. */
export function ledgerTemplate(columns: LedgerColumn[]): string {
  return columns.map(trackFor).join(" ")
}

/**
 * Adapt a legacy px template so fixed content columns also flex:
 * `minmax(0, Xfr)` gets a content floor and double weight, fixed px ≥ 56
 * becomes `minmax(px, px/240 fr)`; smaller tracks (icons, actions) stay fixed.
 */
export function adaptLegacyTemplate(template: string): string {
  return template
    .trim()
    .split(/\s+(?![^()]*\))/)
    .map((token) => {
      const flex = token.match(/^minmax\(0(?:px)?,\s*([\d.]+)fr\)$/)
      if (flex) {
        // Fluid columns stay dominant: twice the weight of any fixed track.
        const weight = Number(flex[1])
        return `minmax(${Math.round(weight * 120)}px, ${weight * 2}fr)`
      }
      const px = token.match(/^(\d+)px$/)
      if (px) {
        const n = Number(px[1])
        return n >= 56 ? `minmax(${n}px, ${(n / 240).toFixed(2)}fr)` : token
      }
      return token
    })
    .join(" ")
}

const LedgerContext = React.createContext<{ template: string; trailingActions: boolean }>({ template: "minmax(0,1fr)", trailingActions: false })

export function Ledger({
  columns,
  className,
  children,
  ...props
}: { columns: string | LedgerColumn[] } & React.HTMLAttributes<HTMLDivElement>) {
  const value = React.useMemo(
    () => ({
      template: typeof columns === "string" ? adaptLegacyTemplate(columns) : ledgerTemplate(columns),
      trailingActions: typeof columns !== "string" && columns[columns.length - 1]?.kind === "actions",
    }),
    [columns],
  )
  return (
    <LedgerContext.Provider value={value}>
      <div className={cn("min-h-0 flex-1 overflow-y-auto", className)} {...props}>
        {children}
      </div>
    </LedgerContext.Provider>
  )
}

function useColumns() {
  return React.useContext(LedgerContext)
}

/* Rows and the header share one gutter: 24px each side, the topbar's
   padding, so the first and last columns of every ledger sit on the same
   two edges page after page. A trailing (zero-width) actions track still
   carries the 10px column gap, which the right padding absorbs. */
function gutterClass(trailingActions: boolean) {
  return trailingActions ? "pl-6 pr-3.5" : "px-6"
}

export function LedgerHeader({ children, className }: { children: React.ReactNode; className?: string }) {
  const { template, trailingActions } = useColumns()
  return (
    <div
      aria-hidden="true"
      style={{ gridTemplateColumns: template }}
      className={cn(
        "sticky top-0 z-[1] grid h-7 items-center gap-x-2.5 border-b border-line bg-surface text-xs text-fg-muted [&>*]:min-w-0 [&>*]:truncate [&>*]:whitespace-nowrap",
        gutterClass(trailingActions),
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
        className="flex h-7 w-full items-center gap-1.5 border-b border-line pl-[22px] pr-6 text-left text-xs text-fg-muted hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"
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
  const { template, trailingActions } = useColumns()
  return (
    <li
      ref={ref}
      role="option"
      aria-selected={selected}
      tabIndex={0}
      style={{ gridTemplateColumns: template }}
      className={cn(
        "group relative grid h-9 cursor-default items-center gap-x-2.5 border-b border-line text-sm text-fg outline-none transition-colors duration-150 ease-settle hover:app-hover focus-visible:app-hover [&>*]:min-w-0 [&>*]:self-center",
        gutterClass(trailingActions),
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
