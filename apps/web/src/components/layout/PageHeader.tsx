import type { ReactNode } from "react"

interface PageHeaderProps {
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  backLink?: ReactNode
}

export function PageHeader({ title, description, action, backLink }: PageHeaderProps) {
  return (
    <div className="mb-8 flex flex-col gap-2">
      {backLink && <div className="text-xs text-fg-subtle">{backLink}</div>}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1.5">
          <h1 className="font-display text-3xl font-semibold leading-tight tracking-tight text-fg">{title}</h1>
          {description && (
            <div className="max-w-2xl text-sm leading-relaxed text-fg-subtle">{description}</div>
          )}
        </div>
        {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
      </div>
    </div>
  )
}
