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

const toneClasses: Record<ActionTone, string> = {
  neutral: "text-slate-500 hover:bg-slate-100 hover:text-slate-950",
  primary: "text-slate-600 hover:bg-slate-100 hover:text-slate-950",
  success: "text-emerald-700 hover:bg-emerald-50 hover:text-emerald-800",
  danger: "text-slate-500 hover:bg-red-50 hover:text-red-700",
}

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
              "inline-flex h-7 w-7 items-center justify-center rounded-md border border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 disabled:pointer-events-none disabled:opacity-45",
              toneClasses[tone],
              className,
            )}
            onClick={(event) => {
              if (stopPropagation) event.stopPropagation()
              onClick?.(event)
            }}
            {...props}
          >
            <CurrentIcon className={cn("h-3.5 w-3.5", busy && "animate-spin")} strokeWidth={1.8} />
          </button>
        </span>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="z-50 rounded-md border border-slate-200 bg-white px-2 py-1 text-[13px] text-slate-600 shadow-md">
          {label}
          <Tooltip.Arrow className="fill-white" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

export function RowActions({
  children,
  className,
}: {
  children: React.ReactNode
  className?: string
}) {
  return (
    <Tooltip.Provider delayDuration={150}>
      <div className={cn("flex min-h-7 items-center justify-end gap-1", className)}>
        {children}
      </div>
    </Tooltip.Provider>
  )
}
