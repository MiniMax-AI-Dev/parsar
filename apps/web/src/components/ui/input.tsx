import * as React from "react"
import { cn } from "../../lib/utils"

/**
 * The one text field. 28px, paper, strong hairline, control shadow;
 * indigo border + ring on focus. Pair with <Kbd /> for shortcut hints.
 */
export const Input = React.forwardRef<
  HTMLInputElement,
  Omit<React.InputHTMLAttributes<HTMLInputElement>, "size"> & {
    /** `lg` (36px) is for the entry surfaces; the console stays at 28px. */
    size?: "default" | "lg"
  }
>(({ className, type, size = "default", ...props }, ref) => {
  return (
    <input
      type={type}
      className={cn(
        "app-shadow-control flex w-full rounded-md border border-line-strong bg-surface text-sm",
        "text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        size === "lg" ? "h-9 px-3 text-base" : "h-7 px-2",
        className,
      )}
      ref={ref}
      {...props}
    />
  )
})
Input.displayName = "Input"
