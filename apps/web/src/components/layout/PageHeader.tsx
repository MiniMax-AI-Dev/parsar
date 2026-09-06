import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"

interface PageHeaderProps {
  /** Omit on a detail whose back link already says where you are. */
  title?: ReactNode
  /** Quiet secondary name beside the title (e.g. the English page name). */
  subtitle?: ReactNode
  /**
   * i18n key (admin namespace) whose en-US value is shown as the subtitle
   * when it differs from the rendered title: the one way console pages get
   * their English name, so every page carries the same title form.
   */
  subtitleFor?: string
  /**
   * @deprecated The design system has no page descriptions ("no helper
   * copy"). Accepted so older call sites still type-check; never rendered.
   */
  description?: ReactNode
  action?: ReactNode
  backLink?: ReactNode
  className?: string
}

/**
 * The 64px topbar every console page starts with: title on the left
 * (the only 600 weight on the screen), actions on the right. Sticks to
 * the top of the scrolling main column and spans its full width.
 */
export function PageHeader({ title, subtitle, subtitleFor, action, backLink, className }: PageHeaderProps) {
  const { t } = useTranslation("admin")
  const english = subtitleFor ? (t(subtitleFor as never, { lng: "en-US" }) as unknown as string) : undefined
  const resolvedSubtitle = subtitle ?? (english && english !== title && english !== subtitleFor ? english : undefined)
  return (
    <header
      className={cn(
        "sticky top-0 z-10 -mx-6 mb-8 flex h-16 shrink-0 items-center gap-3 border-b border-line bg-surface px-6",
        className,
      )}
    >
      {backLink && <div className="shrink-0 text-xs text-fg-muted">{backLink}</div>}
      {title && (
        <h1 className="font-display flex min-w-0 items-baseline gap-2 text-xl leading-none text-fg">
          <span className="truncate">{title}</span>
          {resolvedSubtitle && (
            <span className="shrink-0 text-xs font-normal tracking-normal text-fg-muted">{resolvedSubtitle}</span>
          )}
        </h1>
      )}
      {action && <div className="ml-auto flex shrink-0 items-center gap-2">{action}</div>}
    </header>
  )
}
