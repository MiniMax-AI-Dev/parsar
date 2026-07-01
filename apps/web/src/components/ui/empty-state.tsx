import type { LucideIcon } from "lucide-react"
import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

interface EmptyStateProps {
  icon?: LucideIcon
  title: string
  description?: string
  action?: ReactNode
  className?: string
  /** Show the branded geometric illustration instead of the icon chip.
   *  Use on primary first-run empty states (e.g. "no agents yet"). */
  illustration?: boolean
}

export function EmptyState({ icon: Icon, title, description, action, className, illustration }: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-4 rounded-lg border border-dashed border-line bg-surface px-6 py-16 text-center",
        className
      )}
    >
      {illustration ? (
        <img src="/brand/empty-state.png" alt="" className="h-28 w-28 opacity-90" />
      ) : Icon ? (
        <div className="rounded-full bg-surface-muted p-3 text-fg-subtle">
          <Icon className="h-5 w-5" />
        </div>
      ) : null}
      <div className="space-y-1.5">
        <p className="text-[15px] font-medium text-fg">{title}</p>
        {description && (
          <p className="text-sm text-fg-subtle max-w-sm">{description}</p>
        )}
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  )
}
