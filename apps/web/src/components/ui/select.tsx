import * as SelectPrimitive from "@radix-ui/react-select"
import * as React from "react"
import { Check, ChevronDown, ChevronUp } from "lucide-react"
import { cn } from "../../lib/utils"
import { menuContentClass, menuItemClass } from "./menu"

type SelectProps = Omit<React.ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger>, "value" | "defaultValue" | "onChange"> & {
  value?: string | number
  defaultValue?: string | number
  onValueChange?: (value: string) => void
  wrapperClassName?: string
}

// Prefix every value so an empty application value remains a selectable item.
const itemValue = (value: string | number) => `value:${value}`

export const Select = React.forwardRef<HTMLButtonElement, SelectProps>(
  ({ value, defaultValue, onValueChange, disabled, className, wrapperClassName, children, ...props }, ref) => (
    <SelectPrimitive.Root
      value={value === undefined ? undefined : itemValue(value)}
      defaultValue={defaultValue === undefined ? undefined : itemValue(defaultValue)}
      onValueChange={(next) => {
        // The native form bridge emits an unencoded empty value while options load.
        if (next.startsWith("value:")) onValueChange?.(next.slice(6))
      }}
      disabled={disabled}
    >
      <span className={cn("relative inline-flex min-w-0 w-full", wrapperClassName)}>
        <SelectPrimitive.Trigger
          ref={ref}
          className={cn(
            "app-shadow-control flex h-7 w-full min-w-0 items-center justify-between gap-2 rounded-md border border-line-strong bg-surface px-2 text-left text-sm text-fg transition-[border-color,box-shadow] duration-150 ease-settle focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
            className,
          )}
          {...props}
        >
          <span className="min-w-0 truncate"><SelectPrimitive.Value /></span>
          <SelectPrimitive.Icon asChild>
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          </SelectPrimitive.Icon>
        </SelectPrimitive.Trigger>
      </span>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          position="popper"
          sideOffset={4}
          collisionPadding={8}
          className={cn(menuContentClass, "w-[var(--radix-select-trigger-width)] max-w-[var(--radix-select-content-available-width)] max-h-[min(18rem,var(--radix-select-content-available-height))]")}
          onClick={(event) => event.stopPropagation()}
        >
          <SelectPrimitive.ScrollUpButton className="flex justify-center py-1 text-fg-muted">
            <ChevronUp className="h-3.5 w-3.5" aria-hidden="true" />
          </SelectPrimitive.ScrollUpButton>
          <SelectPrimitive.Viewport>{children}</SelectPrimitive.Viewport>
          <SelectPrimitive.ScrollDownButton className="flex justify-center py-1 text-fg-muted">
            <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
          </SelectPrimitive.ScrollDownButton>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  ),
)
Select.displayName = "Select"

export function SelectOption({ value, children, disabled }: { value: string | number; children: React.ReactNode; disabled?: boolean }) {
  return (
    <SelectPrimitive.Item value={itemValue(value)} disabled={disabled} className={cn(menuItemClass, "relative items-start pl-7 [overflow-wrap:anywhere] data-[disabled]:pointer-events-none data-[disabled]:opacity-50")}>
      <SelectPrimitive.ItemIndicator className="absolute left-2 top-2">
        <Check className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
      </SelectPrimitive.ItemIndicator>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    </SelectPrimitive.Item>
  )
}
