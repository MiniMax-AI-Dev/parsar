import { cn } from "../../lib/utils"

export type StatusKind =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted"

const colorClass: Record<StatusKind, string> = {
  queued: "text-status-queued",
  running: "text-status-running",
  completed: "text-status-completed",
  failed: "text-status-failed",
  cancelled: "text-status-cancelled",
  interrupted: "text-status-interrupted",
}

/**
 * The 14px status icon: the only place status colour lives. Queued is a
 * dashed ring, running a rotating three-quarter arc on a grey track,
 * completed a filled disc with a check, failed a filled disc with an x,
 * cancelled a ring with a slash, interrupted a ring with a dash.
 */
export function StatusIcon({
  status,
  className,
  title,
}: {
  status: StatusKind
  className?: string
  title?: string
}) {
  const common = {
    className: cn("h-3.5 w-3.5 shrink-0", colorClass[status], className),
    viewBox: "0 0 14 14",
    "aria-hidden": title ? undefined : true,
    role: title ? "img" : undefined,
  }
  const label = title ? <title>{title}</title> : null

  switch (status) {
    case "queued":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2.2 1.9" strokeLinecap="round" />
        </svg>
      )
    case "running":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="var(--color-status-track)" strokeWidth="1.5" />
          <g className="origin-center animate-spin-slow motion-reduce:animate-none">
            <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeDasharray="25.9 34.56" transform="rotate(-90 7 7)" />
          </g>
        </svg>
      )
    case "completed":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="6.25" fill="currentColor" />
          <path d="m4.4 7.2 1.8 1.8 3.5-3.7" fill="none" stroke="var(--color-surface)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      )
    case "failed":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="6.25" fill="currentColor" />
          <path d="m4.9 4.9 4.2 4.2M9.1 4.9 4.9 9.1" fill="none" stroke="var(--color-surface)" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      )
    case "cancelled":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M3.9 10.1 10.1 3.9" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      )
    case "interrupted":
      return (
        <svg {...common}>
          {label}
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M4.6 7h4.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      )
  }
}
