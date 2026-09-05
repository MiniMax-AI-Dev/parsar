import * as React from "react"
import { cn } from "../../lib/utils"

/**
 * The one text field. 28px, paper, strong hairline, control shadow;
 * indigo border + ring on focus. Pair with <Kbd /> for shortcut hints.
 */
export const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(({ className, type, ...props }, ref) => {
  return (
    <input
      type={type}
      className={cn(
        "app-shadow-control flex h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        className,
      )}
      ref={ref}
      {...props}
    />
  )
})
Input.displayName = "Input"
