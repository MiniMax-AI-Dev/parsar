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

/**
 * The right-hand detail rail: 384px by default, draggable 320–640, panel tone,
 * one hairline on the left. Header (48px) · scrolling body · footer.
 * Enters with the spring ease once per selection. The expand button in
 * the header lifts the same content into a centred 70% modal with a
 * fly-in from the rail's side; the modal's only control is collapse.
 */
export function DetailRail({
  header,
  footer,
  onClose,
  closeLabel,
  expandable = true,
  children,
  className,
  ...props
}: {
  header: ReactNode
  footer?: ReactNode
  onClose?: () => void
  closeLabel?: string
  /** Show the expand-to-modal button (default true). */
  expandable?: boolean
  children: ReactNode
  className?: string
} & Omit<React.HTMLAttributes<HTMLElement>, "title">) {
  const { t } = useTranslation("common")
  const [expanded, setExpanded] = React.useState(false)
  // Close plays rail-out on the panel and only then unmounts via onClose.
  const [closing, setClosing] = React.useState(false)
  const requestClose = () => {
    if (!onClose) return
    setExpanded(false)
    setClosing(true)
  }
  const rail = useResizableWidth({ storageKey: "rail", defaultWidth: 384, min: 320, max: 640, edge: "left" })
  const expandLabel = t("actions.expand")
  const collapseLabel = t("actions.collapse")
  const resolvedCloseLabel = closeLabel ?? t("actions.close", { defaultValue: "Close" })
  const setOpen = (next: boolean) => setExpanded(next)

  const frame = (mode: "rail" | "modal") => (
    <>
      <div className="flex h-16 shrink-0 items-center gap-2 border-b border-line pl-4 pr-2">
        <div className="flex min-w-0 flex-1 items-center gap-2">{header}</div>
        {expandable && (
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setOpen(mode === "rail")}
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
            onClick={requestClose}
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

  return (
    <>
      <div
        className={cn("relative flex shrink-0", rail.restoring && "transition-[width] duration-[420ms] ease-spring")}
        style={{ width: rail.width }}
      >
        <aside
          className={cn(
            "flex w-full flex-col overflow-hidden border-l border-line bg-surface-subtle",
            closing ? "animate-rail-out" : "animate-rail-in",
            className,
          )}
          onAnimationEnd={(e) => {
            if (closing && e.target === e.currentTarget) onClose?.()
          }}
          {...props}
        >
          {frame("rail")}
        </aside>
        <ResizeHandle edge="left" dragging={rail.dragging} label={t("layout.adjusted")} {...rail.handleProps} />
        <LayoutPrompt open={rail.dirty} onSave={rail.save} onTemporary={rail.keepTemporary} onRestore={rail.restore} />
      </div>

      <DialogPrimitive.Root open={expanded} onOpenChange={setOpen}>
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
