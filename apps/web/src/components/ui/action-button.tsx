import * as React from "react"
import * as Tooltip from "@radix-ui/react-tooltip"
import { Loader2, type LucideIcon } from "lucide-react"

import { cn } from "../../lib/utils"

type ActionTone = "neutral" | "primary" | "success" | "danger"

interface ActionIconButtonProps
  extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  icon: LucideIcon
  label: string
  tone?: ActionTone
  busy?: boolean
  stopPropagation?: boolean
}

/* Tone never colours the resting icon (state lives in icons that mean
   status, not in action buttons); danger only tints the hover. */
const toneClasses: Record<ActionTone, string> = {
  neutral: "text-fg-muted hover:app-hover hover:text-fg",
  primary: "text-fg-muted hover:app-hover hover:text-fg",
  success: "text-fg-muted hover:app-hover hover:text-fg",
  danger: "text-fg-muted hover:app-hover hover:text-status-failed",
}

/** 28px ghost icon button with a tooltip label; the row-action idiom. */
export function ActionIconButton({
  icon: Icon,
  label,
  tone = "neutral",
  busy = false,
  disabled,
  stopPropagation = true,
  className,
  onClick,
  type = "button",
  ...props
}: ActionIconButtonProps) {
  const CurrentIcon = busy ? Loader2 : Icon

  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <span className="inline-flex">
          <button
            type={type}
            aria-label={label}
            disabled={disabled || busy}
            className={cn(
              "inline-flex h-7 w-7 items-center justify-center rounded-md active:scale-[0.97] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:pointer-events-none disabled:opacity-50",
              toneClasses[tone],
              className,
            )}
            onClick={(event) => {
              if (stopPropagation) event.stopPropagation()
              onClick?.(event)
            }}
            {...props}
          >
            <CurrentIcon className={cn("h-3.5 w-3.5", busy && "animate-spin")} strokeWidth={1.5} aria-hidden="true" />
          </button>
        </span>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          sideOffset={4}
          className="app-shadow-floating z-50 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg animate-pop-in data-[state=closed]:animate-pop-out"
        >
          {label}
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

/**
 * Row action cluster. Inside a `LedgerRow` (a `group`) it floats over the
 * row's right end, invisible until the row is hovered or focused, so the
 * last content column of every ledger ends on the same edge and rows read
 * as content, not as toolbars; pass `always` to keep it visible.
 */
export function RowActions({
  children,
  className,
  always = false,
}: {
  children: React.ReactNode
  className?: string
  always?: boolean
}) {
  return (
    <Tooltip.Provider delayDuration={150}>
      <div
        className={cn(
          "flex min-h-7 items-center justify-end gap-0.5",
          "group-[.grid]:absolute group-[.grid]:right-4 group-[.grid]:top-1/2 group-[.grid]:-translate-y-1/2 group-[.grid]:rounded-md group-[.grid]:app-hover-solid group-[.grid]:px-0.5",
          !always && "opacity-0 transition-opacity duration-150 ease-settle focus-within:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100",
          className,
        )}
      >
        {children}
      </div>
    </Tooltip.Provider>
  )
}
