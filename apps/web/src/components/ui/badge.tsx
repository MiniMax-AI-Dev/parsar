import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

/**
 * The one chip. Text stays in ink; the variant only colours the 6px dot,
 * so state is signalled by an icon, never by coloured text.
 */
const badgeVariants = cva(
  "inline-flex h-5 items-center gap-1.5 whitespace-nowrap rounded-md border px-1.5 text-xs text-fg",
  {
    variants: {
      variant: {
        success: "border-line-strong bg-surface",
        warning: "border-line-strong bg-surface",
        destructive: "border-line-strong bg-surface",
        neutral: "border-line-strong bg-surface text-fg-muted",
        primary: "border-line-strong bg-surface",
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
    success: "bg-status-completed",
    warning: "bg-status-running",
    destructive: "bg-status-failed",
    neutral: "bg-status-queued",
    primary: "bg-accent",
  }[variant ?? "neutral"]

  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props}>
      {dot && (
        <span className="relative flex h-1.5 w-1.5 shrink-0">
          {pulse && (
            <span
              className={cn(
                "absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 motion-reduce:hidden",
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
