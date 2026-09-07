import type * as React from "react"

import { cn } from "../../lib/utils"

/**
 * Text the machine wrote: a server message, a command, a payload, a log.
 *
 * It sits in a recessed block rather than running on as loose mono text. The
 * fill is what gives it a level — a raw string like
 * `migration 0042: relation "agent_runs" already exists` next to human copy at
 * the same tone reads as one flat paragraph, and the reader cannot tell which
 * half was written for them. One tone step down says "this came from the
 * system" without a border, a colour or a label.
 *
 * The default cap is 160px; pass `max-h-*` for a block meant to be read at
 * length. It always wraps, because a horizontal scrollbar hides the end of the
 * one sentence that matters.
 */
export function MachineText({
  children,
  className,
  ...props
}: React.HTMLAttributes<HTMLPreElement>) {
  return (
    <pre
      className={cn(
        "m-0 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg",
        className,
      )}
      {...props}
    >
      {children}
    </pre>
  )
}
