import { AlertTriangle, RefreshCw } from "lucide-react"
import type { ReactNode } from "react"
import { cn } from "../../lib/utils"
import { Button } from "./button"

interface ErrorStateProps {
  title?: string
  description?: string
  hint?: string
  onRetry?: () => void
  action?: ReactNode
  className?: string
}

export function ErrorState({
  title = "无法加载数据",
  description,
  hint,
  onRetry,
  action,
  className,
}: ErrorStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-start gap-3 rounded-lg border border-red-200 bg-red-50/40 p-4 text-sm",
        className
      )}
    >
      <div className="flex items-start gap-3">
        <div className="rounded-full bg-red-100 p-1.5 text-red-700">
          <AlertTriangle className="h-4 w-4" />
        </div>
        <div className="space-y-1">
          <p className="font-medium text-red-900">{title}</p>
          {description && <p className="text-red-700">{description}</p>}
          {hint && <p className="text-xs text-red-600">{hint}</p>}
        </div>
      </div>
      {(onRetry || action) && (
        <div className="ml-9 flex items-center gap-2">
          {onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              <RefreshCw className="h-3.5 w-3.5" />
              重试
            </Button>
          )}
          {action}
        </div>
      )}
    </div>
  )
}
