import type * as React from "react"

import { cn } from "../../lib/utils"

/**
 * Content shown exactly as it was written, in monospace, one tone step down:
 * a server message, a command, a JSON payload, a log, a skill's instruction, an
 * agent's system prompt.
 *
 * The fill is what gives it a level. A raw string like
 * `migration 0042: relation "agent_runs" already exists` set next to our own
 * copy at the same tone reads as one flat paragraph, and the reader cannot tell
 * which half was written for them. The recess says "this is quoted, not
 * addressed to you" without a border, a colour or a label.
 *
 * It states no height of its own — a five-line server message and a 400-line
 * skill file want different answers, and a default that nearly every caller
 * overrides is a decision that has not been made. Pass `max-h-*` where the
 * block needs a ceiling; it scrolls once it has one.
 *
 * Wrapping is `break-words`, not `break-all`: it breaks a string that cannot
 * fit on a line of its own — a token, a URL, a base64 blob — and leaves words
 * that would have fit alone.
 */
export function VerbatimBlock({
  children,
  className,
  ...props
}: React.HTMLAttributes<HTMLPreElement>) {
  return (
    <pre
      className={cn(
        "m-0 overflow-auto whitespace-pre-wrap break-words rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg",
        className,
      )}
      {...props}
    >
      {children}
    </pre>
  )
}
