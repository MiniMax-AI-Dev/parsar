/* eslint-disable react-refresh/only-export-components */
import type { ReactNode } from "react"
import { ArrowLeft, ArrowUpRight } from "lucide-react"


export { InlineNotice } from "../../../components/ui/error-state"

/** External link rendered as a property value: ink, underline on hover, trailing arrow. */
export function ExternalLinkValue({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex min-w-0 items-center gap-1 text-fg underline-offset-4 hover:underline"
    >
      <span className="truncate">{children}</span>
      <ArrowUpRight className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
    </a>
  )
}

export function safeExternalURL(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const url = new URL(value)
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : undefined
  } catch {
    return undefined
  }
}

/** Topbar back link: 12px muted, arrow, ink on hover. */
export function BackLink({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1 rounded text-xs text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
    >
      <ArrowLeft className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
      {label}
    </button>
  )
}
