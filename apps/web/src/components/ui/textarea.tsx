import * as React from "react"
import { cn } from "../../lib/utils"

/** Multi-line field styled like Input: paper, strong hairline, control shadow, indigo focus. */
export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => {
  return (
    <textarea
      ref={ref}
      className={cn(
        "app-shadow-control flex min-h-[72px] w-full rounded-md border border-line-strong bg-surface px-2 py-1.5 text-sm leading-relaxed text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        className,
      )}
      {...props}
    />
  )
})
Textarea.displayName = "Textarea"
