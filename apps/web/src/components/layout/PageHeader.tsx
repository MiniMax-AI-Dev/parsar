import type { ReactNode } from "react"

interface PageHeaderProps {
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  backLink?: ReactNode
}

export function PageHeader({ title, description, action, backLink }: PageHeaderProps) {
  return (
    <div className="mb-6 flex flex-col gap-1.5">
      {backLink && <div className="text-xs text-slate-500">{backLink}</div>}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-[22px] font-semibold leading-tight tracking-display text-slate-900">{title}</h1>
          {description && (
            <div className="max-w-2xl text-[13px] leading-relaxed text-slate-500">{description}</div>
          )}
        </div>
        {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
      </div>
    </div>
  )
}
