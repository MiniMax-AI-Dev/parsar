/* eslint-disable react-refresh/only-export-components */
import type { ReactNode } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ListFilter } from "lucide-react"

import { Button } from "./button"
import { menuContentClass, menuItemClass } from "./menu"
import { cn } from "../../lib/utils"

/**
 * The one filter control: an outline trigger that names the facet in play,
 * over a menu of single-choice groups and toggles. Pages compose the facets;
 * the shell — trigger, floating panel, item metrics, the check indicator —
 * lives here so two lists never drift into two filter designs.
 */
const CONTENT_CLASS = cn(menuContentClass, "min-w-[220px]")
const ITEM_CLASS = menuItemClass

export const FilterGroup = DropdownMenu.RadioGroup

export function FilterMenu({ label, summary, children }: {
  label: string
  /** What is filtered right now, shown muted after the label. */
  summary?: string | null
  children: ReactNode
}) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <Button variant="outline" aria-haspopup="menu">
          <ListFilter strokeWidth={1.5} aria-hidden="true" />
          {label}
          {summary && <span className="text-fg-muted">· {summary}</span>}
        </Button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={6} className={CONTENT_CLASS}>
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/**
 * One choice. `count` turns the menu into the distribution as well as the
 * filter: how many rows each value holds, including the zeroes — a value with
 * nothing on it is usually the thing you opened the menu to check.
 */
export function FilterOption({ value, label, count }: {
  value: string
  label: string
  count?: number
}) {
  return (
    <DropdownMenu.RadioItem value={value} className={ITEM_CLASS}>
      <span className="flex-1">{label}</span>
      {count !== undefined && (
        <span className={cn("tabular-nums text-xs", count === 0 ? "text-fg-muted" : "text-fg")}>{count}</span>
      )}
      {/* The indicator's slot is always there so the counts hold one column
          and a row does not nudge its own number when you pick it. */}
      <span className="flex h-3.5 w-3.5 shrink-0 items-center justify-center">
        <DropdownMenu.ItemIndicator>
          <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
        </DropdownMenu.ItemIndicator>
      </span>
    </DropdownMenu.RadioItem>
  )
}

export function FilterToggle({ checked, onCheckedChange, label }: {
  checked: boolean
  onCheckedChange: (next: boolean) => void
  label: string
}) {
  return (
    <DropdownMenu.CheckboxItem checked={checked} onCheckedChange={onCheckedChange} className={ITEM_CLASS}>
      <span className="flex-1">{label}</span>
      <span className="flex h-3.5 w-3.5 shrink-0 items-center justify-center">
        <DropdownMenu.ItemIndicator>
          <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
        </DropdownMenu.ItemIndicator>
      </span>
    </DropdownMenu.CheckboxItem>
  )
}

export function FilterSeparator() {
  return <DropdownMenu.Separator className="my-1 h-px bg-line" />
}
