import type { ReactNode } from "react"
import { cn } from "../../lib/utils"

/** Keyboard key chip: 12px muted on paper, strong hairline, heavier bottom edge. */
export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        "inline-block min-w-5 rounded border border-b-[1.5px] border-line-strong bg-surface px-1 py-0.5 text-center font-sans text-xs leading-none text-fg-muted",
        className,
      )}
    >
      {children}
    </kbd>
  )
}
