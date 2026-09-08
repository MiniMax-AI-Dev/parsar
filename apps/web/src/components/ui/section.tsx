import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/**
 * The head of a section on a full page: a 16px/500 title with an optional
 * muted count and one right-aligned action.
 *
 * Sixteen, not thirteen: a heading the same size as the rows beneath it — or,
 * as several pages had written it by hand, one pixel *smaller* — is a caption,
 * and the reader has to work out from weight alone where one section ends and
 * the next begins. The page reads 20 · 16 · 13 now, three sizes the eye can
 * separate at a glance.
 *
 * Use it bare when the section's content is not its child (a full-bleed ledger
 * that must span the page); use `PageSection` when it is.
 */
export function SectionHead({
  title,
  meta,
  action,
  className,
}: {
  title: ReactNode
  meta?: ReactNode
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn("mb-2 flex min-h-7 items-center justify-between gap-2", className)}>
      <h2 className="flex items-baseline gap-2 text-lg font-medium text-fg">
        <span>{title}</span>
        {meta !== undefined && <span className="text-xs font-normal tabular-nums text-fg-muted">{meta}</span>}
      </h2>
      {action}
    </div>
  )
}

/**
 * Section of a full-page detail or settings page: a `SectionHead` with the
 * content below it. Sections are separated by spacing only; there is no card.
 *
 * (Inside a `DetailRail` use `RailSection`, whose head stays 12px. That is a
 * deliberate exception, not an oversight: in a 384px panel the head is an
 * eyebrow over a dense property list, a different device from a heading on a
 * page, and 16px there would weigh more than the values it introduces.)
 */
export function PageSection({
  title,
  meta,
  action,
  children,
  className,
}: {
  title: ReactNode
  meta?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("mt-6 first:mt-0", className)}>
      <SectionHead title={title} meta={meta} action={action} />
      {children}
    </section>
  )
}

/**
 * The subject of a detail page: its name at the page-title size with its
 * badges beside it, centred on one line. Detail topbars carry only the
 * back link and the actions, so this is where the reader learns what
 * they are looking at.
 */
export function DetailHeading({
  title,
  badges,
  className,
}: {
  title: ReactNode
  badges?: ReactNode
  className?: string
}) {
  return (
    <div className={cn("mb-3 flex flex-wrap items-center gap-x-2 gap-y-1", className)}>
      <h2 className="font-display min-w-0 truncate text-xl text-fg">{title}</h2>
      {badges}
    </div>
  )
}
