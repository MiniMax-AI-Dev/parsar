import * as React from "react"
import { cn } from "../../lib/utils"

/**
 * Drag handle on a panel edge: a 6px invisible hit area straddling the
 * hairline; the hairline turns accent while hovered or dragged. Keyboard:
 * arrow keys resize in 8px steps (32px with shift).
 */
export function ResizeHandle({
  edge,
  dragging,
  label,
  className,
  ...props
}: {
  edge: "left" | "right"
  dragging?: boolean
  label: string
} & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      tabIndex={0}
      className={cn(
        "group absolute top-0 z-20 h-full w-1.5 cursor-col-resize select-none touch-none focus-visible:outline-none",
        edge === "right" ? "-right-[3px]" : "-left-[3px]",
        className,
      )}
      {...props}
    >
      <span
        aria-hidden="true"
        className={cn(
          "absolute left-1/2 top-0 h-full w-px -translate-x-1/2 transition-colors duration-150 ease-settle group-hover:bg-accent group-focus-visible:bg-accent",
          dragging ? "bg-accent" : "bg-transparent",
        )}
      />
    </div>
  )
}
