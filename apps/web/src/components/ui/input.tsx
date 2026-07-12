import * as React from "react"
import { cn } from "../../lib/utils"

export const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(({ className, type, ...props }, ref) => {
  return (
    <input
      type={type}
      className={cn(
        "flex h-9 w-full rounded-lg border border-line bg-surface px-3 py-1.5 text-sm shadow-sm transition-[border-color,box-shadow,background-color] placeholder:text-fg-faint hover:border-line-strong focus-visible:border-info focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-info/20 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        className,
      )}
      ref={ref}
      {...props}
    />
  )
})
Input.displayName = "Input"
