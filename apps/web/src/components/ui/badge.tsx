import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium transition-colors",
  {
    variants: {
      variant: {
        success: "border-emerald-200 bg-emerald-50 text-emerald-700",
        warning: "border-amber-200 bg-amber-50 text-amber-700",
        destructive: "border-red-200 bg-red-50 text-red-700",
        neutral: "border-slate-200 bg-slate-50 text-slate-600",
        primary: "border-blue-200 bg-blue-50 text-blue-700",
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
    success: "bg-emerald-500",
    warning: "bg-amber-500",
    destructive: "bg-red-500",
    neutral: "bg-slate-400",
    primary: "bg-blue-500",
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
