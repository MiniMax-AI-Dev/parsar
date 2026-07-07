import { useEffect, type RefObject } from "react"

/**
 * Drive an element's vertical scroll from the wheel manually and swallow
 * the event, so it never reaches Radix Dialog's react-remove-scroll or
 * DropdownMenu's own wheel handler.
 *
 * React's onWheel is passive by default and dispatches after document-level
 * capture listeners, so preventDefault/stopPropagation in JSX is unreliable
 * inside a Dialog. Attaching non-passive with capture keeps the wheel local
 * to this element.
 */
export function useWheelScroll(ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      el.scrollTop += e.deltaY
      e.preventDefault()
      e.stopPropagation()
    }
    el.addEventListener("wheel", onWheel, { passive: false })
    return () => el.removeEventListener("wheel", onWheel)
  }, [ref])
}
