/* eslint-disable react-refresh/only-export-components */
import * as React from "react"

import { InlineNotice, type NoticeTone } from "./error-state"
import { cn } from "../../lib/utils"

/**
 * The console's one transient message.
 *
 * A toast is for what may be missed: a save that worked, a delete that did
 * not. What must not be missed stays where it happened — a list that failed to
 * load keeps its `ErrorState`, because a notice that fades would leave the
 * reader facing an empty page, and a field error keeps its `InlineError`,
 * because it belongs beside the input it is about.
 *
 * It borrows the floating-notice vocabulary already in the product rather than
 * inventing a second one: the same paper strip at the top centre, the same
 * hairline and floating shadow, the same `pop-in` / `pop-out`, and the tone
 * carried by a 14px glyph with the text left in ink — never a tinted box.
 */
export interface Toast {
  id: number
  tone: NoticeTone
  message: string
  /** Errors hold longer than confirmations; you have to read a failure. */
  durationMs: number
}

interface ToastContextValue {
  show: (message: string, tone?: NoticeTone) => void
}

const ToastContext = React.createContext<ToastContextValue | null>(null)

/** Nothing to say is the common case, so a caller outside the provider no-ops. */
export function useToast(): ToastContextValue {
  return React.useContext(ToastContext) ?? { show: () => {} }
}

const DEFAULT_MS = 4000
const ERROR_MS = 7000
/** Older messages fall off rather than stacking into a wall. */
const MAX_VISIBLE = 3

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = React.useState<Toast[]>([])
  const nextId = React.useRef(1)

  const dismiss = React.useCallback((id: number) => {
    setToasts((list) => list.filter((toast) => toast.id !== id))
  }, [])

  const show = React.useCallback((message: string, tone: NoticeTone = "success") => {
    const id = nextId.current++
    setToasts((list) => [
      ...list.slice(-(MAX_VISIBLE - 1)),
      { id, tone, message, durationMs: tone === "error" ? ERROR_MS : DEFAULT_MS },
    ])
  }, [])

  const value = React.useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        className="pointer-events-none fixed left-1/2 top-3 z-[60] flex -translate-x-1/2 flex-col items-center gap-2"
        aria-live="polite"
      >
        {toasts.map((toast) => (
          <ToastStrip key={toast.id} toast={toast} onDismiss={() => dismiss(toast.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  )
}

/**
 * One strip. It counts down on its own and stops counting while the pointer is
 * on it or focus is inside — a message you are still reading should not leave
 * mid-sentence.
 */
function ToastStrip({ toast, onDismiss }: { toast: Toast; onDismiss: () => void }) {
  const [leaving, setLeaving] = React.useState(false)
  const [held, setHeld] = React.useState(false)

  React.useEffect(() => {
    if (held || leaving) return
    const timer = window.setTimeout(() => setLeaving(true), toast.durationMs)
    return () => window.clearTimeout(timer)
  }, [held, leaving, toast.durationMs])

  return (
    <div
      role={toast.tone === "error" ? "alert" : "status"}
      onMouseEnter={() => setHeld(true)}
      onMouseLeave={() => setHeld(false)}
      onFocusCapture={() => setHeld(true)}
      onBlurCapture={() => setHeld(false)}
      // Every entrance has an exit: the strip leaves on its own animation and
      // is dropped on animationend, never yanked off screen.
      onAnimationEnd={() => { if (leaving) onDismiss() }}
      className={cn(
        "app-shadow-floating pointer-events-auto flex max-w-[32rem] items-start gap-2 rounded-lg border border-line bg-surface py-1.5 pl-3 pr-3 text-sm text-fg",
        leaving ? "animate-pop-out" : "animate-pop-in",
      )}
    >
      <InlineNotice tone={toast.tone}>{toast.message}</InlineNotice>
    </div>
  )
}
