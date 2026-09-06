import * as React from "react"
import type { ReactNode } from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Maximize2, Minimize2, X } from "lucide-react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { Button } from "./button"
import { ResizeHandle } from "./resize-handle"
import { LayoutPrompt } from "../layout/LayoutPrompt"
import { useResizableWidth } from "../../lib/layout-width"
import { useDetailExpanded } from "../../lib/admin-router"

/**
 * The right-hand detail rail: 384px by default, draggable 320–640, panel tone,
 * one hairline on the left. Header (64px) · scrolling body · footer.
 * Its width animates from and back to zero, so the main column widens and
 * narrows with it instead of jumping once the rail is gone. The expand button in
 * the header lifts the same content into a centred 70% panel; that expanded
 * state lives in the URL (`&view=full`), so it can be linked and dismissed with
 * the browser's back button rather than trapping the reader over the list.
 */
export function DetailRail({
  header,
  footer,
  open = true,
  onClose,
  onClosed,
  closeLabel,
  expandable = true,
  children,
  className,
  ...props
}: {
  header: ReactNode
  footer?: ReactNode
  /** False starts the exit; keep the rail mounted until `onClosed` fires. */
  open?: boolean
  onClose?: () => void
  /** The exit finished — the caller can drop the mount now. */
  onClosed?: () => void
  closeLabel?: string
  /** Show the expand-to-modal button (default true). */
  expandable?: boolean
  children: ReactNode
  className?: string
} & Omit<React.HTMLAttributes<HTMLElement>, "title">) {
  const { t } = useTranslation("common")
  const { expanded, setExpanded } = useDetailExpanded()
  const rail = useResizableWidth({ storageKey: "rail", defaultWidth: 384, min: 320, max: 640, edge: "left" })
  const expandLabel = t("actions.expand")
  const collapseLabel = t("actions.collapse")
  const resolvedCloseLabel = closeLabel ?? t("actions.close", { defaultValue: "Close" })
  const setModalOpen = (next: boolean) => setExpanded(next)

  const frame = (mode: "rail" | "modal") => (
    <>
      <div className="flex h-16 shrink-0 items-center gap-2 border-b border-line pl-4 pr-2">
        <div className="flex min-w-0 flex-1 items-center gap-2">{header}</div>
        {expandable && (
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setModalOpen(mode === "rail")}
            aria-label={mode === "rail" ? expandLabel : collapseLabel}
            title={mode === "rail" ? expandLabel : collapseLabel}
          >
            {mode === "rail" ? <Maximize2 strokeWidth={1.5} /> : <Minimize2 strokeWidth={1.5} />}
          </Button>
        )}
        {onClose && mode === "rail" && (
          <Button
            variant="ghost"
            size="icon"
            onClick={onClose}
            aria-label={resolvedCloseLabel}
            title={resolvedCloseLabel}
          >
            <X strokeWidth={1.5} />
          </Button>
        )}
      </div>
      <div className={cn("min-h-0 flex-1 overflow-y-auto pb-2 pt-4", mode === "rail" ? "px-4" : "px-6")}>
        {children}
      </div>
      {footer && (
        <div className={cn("flex shrink-0 items-center gap-2 border-t border-line py-3", mode === "rail" ? "px-4" : "px-6")}>
          {footer}
        </div>
      )}
    </>
  )

  // The rail's WIDTH is what the main column reads, so it has to animate
  // too: opening from 0 and closing back to 0 makes both columns move as
  // one instead of the rail sliding out and the page jumping after it.
  const [revealed, setRevealed] = React.useState(false)
  React.useLayoutEffect(() => {
    const id = requestAnimationFrame(() => setRevealed(true))
    return () => cancelAnimationFrame(id)
  }, [])
  const collapsed = !open || !revealed
  // A rail on its way out cannot stay expanded: drop the URL state in place,
  // so closing never leaves a `view=full` step to back through.
  React.useEffect(() => {
    if (!open && expanded) setExpanded(false, true)
  }, [open, expanded, setExpanded])
  // Dragging must stay pixel-exact, so the width transition is off while
  // the handle is held; restoring keeps its own longer spring.
  const widthMotion = rail.dragging
    ? undefined
    : rail.restoring
      ? "transition-[width] duration-[420ms] ease-spring"
      : "transition-[width] duration-[260ms] ease-spring"

  return (
    <>
      <div
        className={cn("relative flex shrink-0 overflow-hidden", widthMotion)}
        style={{ width: collapsed ? 0 : rail.width }}
        onTransitionEnd={(e) => {
          // Every closer (the X, a row toggle, the route) flips `open`, so
          // they all play the same exit and unmount at the same moment.
          if (open || e.propertyName !== "width" || e.target !== e.currentTarget) return
          onClosed?.()
        }}
      >
        <aside
          className={cn(
            "flex shrink-0 flex-col overflow-hidden border-l border-line bg-surface-subtle",
            className,
          )}
          style={{ width: rail.width }}
          {...props}
        >
          {frame("rail")}
        </aside>
        <ResizeHandle edge="left" dragging={rail.dragging} label={t("layout.adjusted")} {...rail.handleProps} />
        <LayoutPrompt open={rail.dirty} onSave={rail.save} onTemporary={rail.keepTemporary} onRestore={rail.restore} />
      </div>

      <DialogPrimitive.Root open={expanded && open} onOpenChange={setModalOpen}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-surface-inverse/30 data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out" />
          <DialogPrimitive.Content
            aria-label={typeof props["aria-label"] === "string" ? props["aria-label"] : undefined}
            className="app-modal-center app-shadow-floating fixed left-1/2 top-1/2 z-50 flex h-[70vh] w-[70vw] min-w-[720px] max-w-[1200px] flex-col overflow-hidden rounded-lg border border-line bg-surface-subtle focus:outline-none data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out"
          >
            <DialogPrimitive.Title className="sr-only">{resolvedCloseLabel}</DialogPrimitive.Title>
            {frame("modal")}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    </>
  )
}

/**
 * A ledger and its rail, side by side. The list column flexes and scrolls on
 * its own; the rail sits at the page's right edge and animates its own width,
 * so both columns move together. Every list-with-detail page uses this — the
 * shell is not something a page re-invents.
 */
export function RailLayout({ children, rail }: { children: ReactNode; rail?: ReactNode }) {
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      {rail}
    </div>
  )
}

/** Section heading inside a rail: 12px, 500, ink, optional muted count. */
export function RailSection({
  title,
  meta,
  children,
  className,
}: {
  title: ReactNode
  meta?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("mt-5 first:mt-0", className)}>
      <h3 className="mb-0.5 flex items-center justify-between text-xs font-medium text-fg">
        <span>{title}</span>
        {meta && <span className="font-normal tabular-nums text-fg-muted">{meta}</span>}
      </h3>
      {children}
    </section>
  )
}
