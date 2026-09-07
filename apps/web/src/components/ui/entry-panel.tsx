import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/**
 * The entry surfaces (login, setup, onboarding, invite, join landing):
 * a centred page on the paper ground holding the one floating card in
 * the product (`.app-panel`: hairline, 8px radius, floating shadow).
 */
export function EntryPage({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <main
      className={cn(
        "grid min-h-screen place-items-center bg-surface px-6 py-12 text-fg",
        className,
      )}
    >
      {children}
    </main>
  )
}

/**
 * The panel: a 13px/500 "Parsar" wordmark, an optional 22px/500 title and
 * one muted sentence, then the form. Pass `wordmark={false}` when the
 * title is the wordmark itself (login).
 */
export function EntryPanel({
  title,
  description,
  wordmark = true,
  children,
  className,
}: {
  title?: ReactNode
  description?: ReactNode
  wordmark?: boolean
  children?: ReactNode
  className?: string
}) {
  return (
    <section className={cn("app-panel w-full max-w-[400px] p-6", className)}>
      {(wordmark || title || description) && (
        <header className="mb-5">
          {wordmark && (
            <p translate="no" className="text-sm font-medium text-fg">
              Parsar
            </p>
          )}
          {title && (
            <h1 className={cn("text-2xl font-medium tracking-display text-fg", wordmark && "mt-3")}>
              {title}
            </h1>
          )}
          {description && <p className="mt-1 text-base text-fg-muted">{description}</p>}
        </header>
      )}
      {children}
    </section>
  )
}

/** Form footer: top hairline, message left, buttons right. */
export function EntryFooter({
  message,
  children,
  className,
}: {
  message?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn("mt-4 flex items-center justify-between gap-3 border-t border-line pt-4", className)}>
      <div className="min-w-0 flex-1 text-sm">{message}</div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  )
}
