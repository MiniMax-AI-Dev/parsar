/* eslint-disable react-refresh/only-export-components */
import * as React from "react"
import { createPortal } from "react-dom"

import { InlineNotice, type NoticeTone } from "./error-state"
import { VerbatimBlock } from "./verbatim"
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
 * Everything that floats at the top centre goes through here, including the
 * layout prompt. Two components drawing their own strip at the same coordinate
 * is how one ends up covering the other's buttons; a queue cannot collide with
 * itself.
 *
 * It portals to `document.body` and sits at z-60, one step above the 50 that
 * dialogs and their overlays use. Inside the app root at the same z it would
 * lose to a dialog on tree order alone — Radix appends its portal later — and a
 * message that an overlay dims is a message nobody reads. (Announcement is not
 * the reason: `aria-hidden`'s `hideOthers`, which Radix uses, exempts
 * `[aria-live]` and every ancestor of one, so these regions are spoken whether
 * they sit in the root or in the body.)
 */
export interface ToastOptions {
  tone?: NoticeTone
  /**
   * The machine's version of the message — a server string, an exit code —
   * shown under the sentence in a recessed block. The sentence says what
   * happened; this says what the server said. Keeping them apart is the only
   * way the reader can tell which half was written for them.
   */
  detail?: string
  /** Controls that belong to the message, e.g. save / keep / undo. */
  action?: React.ReactNode
  /** Stay until dismissed. For a message that asks something. */
  persist?: boolean
  /** Same key replaces the live message instead of stacking a second one. */
  key?: string
}

interface ToastItem extends ToastOptions {
  id: number
  message: React.ReactNode
  durationMs: number
  leaving: boolean
}

/**
 * The one way to say something transient. Passed down as a prop wherever a
 * child needs to report the result of its own request, so a failure can carry
 * `tone: "error"` instead of arriving dressed as a success.
 */
export type ShowToast = (message: React.ReactNode, options?: ToastOptions) => void

interface ToastContextValue {
  show: ShowToast
  dismiss: (key: string) => void
}

const ToastContext = React.createContext<ToastContextValue | null>(null)

/** Nothing to say is the common case, so a caller outside the provider no-ops. */
const NO_PROVIDER: ToastContextValue = { show: () => {}, dismiss: () => {} }

export function useToast(): ToastContextValue {
  return React.useContext(ToastContext) ?? NO_PROVIDER
}

const DEFAULT_MS = 4000
/** You have to read a failure, so it holds longer than a confirmation. */
const ERROR_MS = 7000
/** Older messages leave rather than stacking into a wall. */
const MAX_VISIBLE = 3

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = React.useState<ToastItem[]>([])
  const nextId = React.useRef(1)

  const drop = React.useCallback((id: number) => {
    setToasts((list) => list.filter((toast) => toast.id !== id))
  }, [])

  /** Start the exit rather than removing the node, so the strip animates out. */
  const startLeaving = React.useCallback((id: number) => {
    setToasts((list) => list.map((t) => (t.id === id ? { ...t, leaving: true } : t)))
  }, [])

  const show = React.useCallback((message: React.ReactNode, options: ToastOptions = {}) => {
    const id = nextId.current++
    const tone = options.tone ?? "success"
    const fields = {
      message,
      tone,
      detail: options.detail,
      action: options.action,
      persist: options.persist,
      key: options.key,
      durationMs: tone === "error" ? ERROR_MS : DEFAULT_MS,
      leaving: false,
    }
    setToasts((list) => {
      // A keyed message updates the strip that is already standing rather than
      // swapping the node: same id, same mount, so it re-words itself in place
      // instead of playing its entrance again. A strip on its way out is caught
      // and revived by the same path.
      const sameKey = options.key ? list.find((t) => t.key === options.key) : undefined
      if (sameKey) return list.map((t) => (t.id === sameKey.id ? { ...t, ...fields } : t))

      // Over the cap the oldest is asked to leave; it is never yanked, and a
      // message that is a question (`persist`) is never the one asked.
      const live = list.filter((t) => !t.leaving && !t.persist)
      const excess = live.slice(0, Math.max(0, live.length - (MAX_VISIBLE - 1)))
      const next = list.map((t) =>
        excess.some((e) => e.id === t.id) ? { ...t, leaving: true } : t,
      )
      return [...next, { id, ...fields }]
    })
  }, [])

  const dismiss = React.useCallback((key: string) => {
    setToasts((list) => list.map((t) => (t.key === key ? { ...t, leaving: true } : t)))
  }, [])

  const value = React.useMemo(() => ({ show, dismiss }), [show, dismiss])
  const polite = toasts.filter((t) => t.tone !== "error")
  const assertive = toasts.filter((t) => t.tone === "error")

  return (
    <ToastContext.Provider value={value}>
      {children}
      {typeof document !== "undefined" &&
        createPortal(
          <div className="pointer-events-none fixed left-1/2 top-3 z-[60] flex -translate-x-1/2 flex-col items-center">
            {/* Two regions rather than one: a failure interrupts, a
                confirmation waits its turn. Neither strip repeats the role,
                because a live region inside a live region announces twice. */}
            {/* The space between the two regions belongs to the region that
                has something in it: an empty flex child still takes the
                parent's gap, which pushed a lone message 8px off its mark. */}
            <div aria-live="assertive" className="flex flex-col items-center gap-2 [&:not(:empty)]:mb-2">
              {assertive.map((toast) => (
                <ToastStrip key={toast.id} toast={toast} onLeave={startLeaving} onDrop={drop} />
              ))}
            </div>
            <div aria-live="polite" className="flex flex-col items-center gap-2">
              {polite.map((toast) => (
                <ToastStrip key={toast.id} toast={toast} onLeave={startLeaving} onDrop={drop} />
              ))}
            </div>
          </div>,
          document.body,
        )}
    </ToastContext.Provider>
  )
}

/**
 * One strip. It counts down on its own and *pauses* while the pointer is on it
 * or focus is inside — resuming with the time that was left, not from the top,
 * so resting a cursor nearby cannot hold a message on screen forever.
 */
function ToastStrip({ toast, onLeave, onDrop }: {
  toast: ToastItem
  onLeave: (id: number) => void
  onDrop: (id: number) => void
}) {
  const [held, setHeld] = React.useState(false)
  const remaining = React.useRef(toast.durationMs)
  const startedAt = React.useRef(0)

  // Re-worded in place (a keyed message), so the countdown starts over: the
  // reader has not read this one yet. Declared before the countdown so its
  // cleanup has already banked the elapsed time when this runs.
  React.useEffect(() => {
    remaining.current = toast.durationMs
  }, [toast.message, toast.detail, toast.durationMs])

  React.useEffect(() => {
    if (toast.persist || toast.leaving || held) return
    startedAt.current = Date.now()
    const timer = window.setTimeout(() => onLeave(toast.id), remaining.current)
    return () => {
      window.clearTimeout(timer)
      remaining.current = Math.max(0, remaining.current - (Date.now() - startedAt.current))
    }
  }, [held, toast.persist, toast.leaving, toast.id, onLeave, toast.message, toast.detail, toast.durationMs])

  return (
    <div
      onMouseEnter={() => setHeld(true)}
      onMouseLeave={() => setHeld(false)}
      onFocusCapture={() => setHeld(true)}
      onBlurCapture={() => setHeld(false)}
      // Every entrance has an exit: the strip is dropped when its own exit
      // ends, never on a descendant's animation and never mid-flight.
      onAnimationEnd={(e) => {
        if (toast.leaving && e.target === e.currentTarget) onDrop(toast.id)
      }}
      className={cn(
        "app-shadow-floating pointer-events-auto flex w-max max-w-[32rem] flex-col gap-1.5 rounded-lg border border-line bg-surface py-1.5 text-sm text-fg",
        toast.action ? "pl-3 pr-1.5" : "px-3",
        toast.leaving ? "animate-pop-out" : "animate-pop-in",
      )}
    >
      <div className="flex items-center gap-2">
        <InlineNotice tone={toast.tone ?? "success"} announce={false}>
          {toast.message}
        </InlineNotice>
        {toast.action}
      </div>
      {toast.detail && <VerbatimBlock className="max-h-24">{toast.detail}</VerbatimBlock>}
    </div>
  )
}
