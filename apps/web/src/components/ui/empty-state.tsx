import type { LucideIcon } from "lucide-react"
import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

interface EmptyStateProps {
  icon?: LucideIcon
  title: string
  description?: string
  action?: ReactNode
  className?: string
}

/**
 * Flat empty state: a muted icon, an ink title, optional one-line
 * description and one action. No dashed box, no card.
 */
export function EmptyState({ icon: Icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-3 px-6 py-16 text-center",
        className
      )}
    >
      {Icon && <Icon className="h-5 w-5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />}
      <div className="space-y-1">
        <p className="text-sm font-medium text-fg">{title}</p>
        {description && (
          <p className="max-w-sm text-sm text-fg-muted">{description}</p>
        )}
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  )
}
