import { useEffect, useState } from "react"

/**
 * useNow — returns `Date.now()` that re-renders every `intervalMs` ms.
 * Use in components displaying relative timestamps; without it the "Xm ago"
 * label is frozen to mount time. Default 15s matches the typical refetch
 * cadence and avoids second-level render churn.
 */
export function useNow(intervalMs = 15_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [intervalMs])
  return now
}
