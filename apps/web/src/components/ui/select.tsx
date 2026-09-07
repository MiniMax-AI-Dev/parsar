import * as React from "react"
import { ChevronDown } from "lucide-react"
import { cn } from "../../lib/utils"

/**
 * The one native select, styled like Input: 28px, paper, strong hairline,
 * control shadow, 6px radius, indigo focus, a 14px muted chevron.
 */
export const Select = React.forwardRef<
  HTMLSelectElement,
  React.SelectHTMLAttributes<HTMLSelectElement> & { wrapperClassName?: string }
>(({ className, wrapperClassName, children, ...props }, ref) => {
  return (
    <span className={cn("relative inline-flex w-full", wrapperClassName)}>
      <select
        ref={ref}
        className={cn(
          "app-shadow-control h-7 w-full appearance-none rounded-md border border-line-strong bg-surface pl-2 pr-7 text-sm text-fg transition-[border-color,box-shadow] duration-150 ease-settle focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
        strokeWidth={1.5}
        aria-hidden="true"
      />
    </span>
  )
})
Select.displayName = "Select"
