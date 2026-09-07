import * as React from "react"
import { cn } from "../../lib/utils"

/**
 * The ledger table. No outer card: a 28px muted column header with one
 * hairline, 36px rows separated by hairlines, ink text at 13px. Numbers
 * get `text-right tabular-nums` at the call site.
 */
export const Table = React.forwardRef<HTMLTableElement, React.HTMLAttributes<HTMLTableElement>>(
  ({ className, ...props }, ref) => (
    <div className="relative w-full overflow-auto">
      <table ref={ref} className={cn("w-full caption-bottom text-sm text-fg", className)} {...props} />
    </div>
  ),
)
Table.displayName = "Table"

export const TableHeader = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ className, ...props }, ref) => (
  <thead ref={ref} className={cn("border-b border-line", className)} {...props} />
))
TableHeader.displayName = "TableHeader"

export const TableBody = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ className, ...props }, ref) => (
  <tbody ref={ref} className={cn(className)} {...props} />
))
TableBody.displayName = "TableBody"

export const TableRow = React.forwardRef<
  HTMLTableRowElement,
  React.HTMLAttributes<HTMLTableRowElement>
>(({ className, ...props }, ref) => (
  <tr
    ref={ref}
    className={cn(
      "h-9 border-b border-line transition-colors duration-150 ease-settle hover:app-hover data-[state=selected]:app-selected",
      className,
    )}
    {...props}
  />
))
TableRow.displayName = "TableRow"

export const TableHead = React.forwardRef<
  HTMLTableCellElement,
  React.ThHTMLAttributes<HTMLTableCellElement>
>(({ className, ...props }, ref) => (
  <th
    ref={ref}
    className={cn("h-7 whitespace-nowrap px-3 text-left align-middle text-xs font-normal text-fg-muted", className)}
    {...props}
  />
))
TableHead.displayName = "TableHead"

export const TableCell = React.forwardRef<
  HTMLTableCellElement,
  React.TdHTMLAttributes<HTMLTableCellElement>
>(({ className, ...props }, ref) => (
  <td ref={ref} className={cn("px-3 py-1.5 align-middle text-sm text-fg", className)} {...props} />
))
TableCell.displayName = "TableCell"
