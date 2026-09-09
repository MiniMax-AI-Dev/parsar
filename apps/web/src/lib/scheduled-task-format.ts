import type { StatusKind } from "../components/ui/status-icon"

function pad(n: number): string {
  return String(n).padStart(2, "0")
}

/** The last run's outcome as the ledger's status icon; a task that never ran is "queued". */
export function scheduledTaskStatusIcon(status: string): StatusKind {
  switch (status) {
    case "running":
      return "running"
    case "completed":
      return "completed"
    case "failed":
      return "failed"
    case "cancelled":
    case "skipped_overlap":
      return "cancelled"
    case "interrupted":
    case "auto_disabled":
      return "interrupted"
    default:
      return "queued"
  }
}

export function formatScheduledTaskTime(iso: string | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

