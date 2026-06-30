import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium transition-colors",
  {
    variants: {
      variant: {
        success: "border-success-border bg-success-subtle text-success",
        warning: "border-warning-border bg-warning-subtle text-warning",
        destructive: "border-danger-border bg-danger-subtle text-danger-emphasis",
        neutral: "border-line bg-surface-subtle text-fg-muted",
        primary: "border-info-border bg-info-subtle text-info",
      },
    },
    defaultVariants: {
      variant: "neutral",
    },
  }
)

interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {
  dot?: boolean
  /** Animate the dot (use for live/in-progress states like "running"). */
  pulse?: boolean
}

export function Badge({ className, variant, dot, pulse, children, ...props }: BadgeProps) {
  const dotColor = {
    success: "bg-success",
    warning: "bg-warning",
    destructive: "bg-danger",
    neutral: "bg-surface-muted",
    primary: "bg-info",
  }[variant ?? "neutral"]

  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props}>
      {dot && (
        <span className="relative flex h-1.5 w-1.5">
          {pulse && (
            <span
              className={cn(
                "absolute inline-flex h-full w-full animate-ping rounded-full opacity-75",
                dotColor
              )}
            />
          )}
          <span className={cn("relative inline-flex h-1.5 w-1.5 rounded-full", dotColor)} />
        </span>
      )}
      {children}
    </span>
  )
}
