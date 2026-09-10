import { useLayoutEffect, useRef } from "react"

export function useLedgerScrollAnchor() {
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const viewport = ref.current
    if (!viewport) return
    let width = viewport.clientWidth
    let scrollTop = viewport.scrollTop
    let anchor: { row: HTMLElement; offset: number } | undefined
    const remember = (target?: EventTarget | null) => {
      const bounds = viewport.getBoundingClientRect()
      const clicked = target instanceof Element ? target.closest<HTMLElement>('[role="option"]') : null
      const row = clicked && viewport.contains(clicked) ? clicked :
        Array.from(viewport.querySelectorAll<HTMLElement>('[role="option"]'))
          .find((item) => item.getBoundingClientRect().bottom > bounds.top)
      anchor = row ? { row, offset: row.getBoundingClientRect().top - bounds.top } : undefined
    }
    const onSelect = (event: Event) => remember(event.target)
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Enter" || event.key === " ") remember(event.target)
    }
    const onScroll = () => {
      if (viewport.clientWidth !== width || viewport.scrollTop === scrollTop) return
      scrollTop = viewport.scrollTop
      remember()
    }
    const observer = new ResizeObserver(() => {
      if (viewport.clientWidth === width) return
      width = viewport.clientWidth
      if (anchor && viewport.contains(anchor.row)) {
        const offset = anchor.row.getBoundingClientRect().top - viewport.getBoundingClientRect().top
        viewport.scrollTop += offset - anchor.offset
        scrollTop = viewport.scrollTop
      } else remember()
    })
    remember()
    observer.observe(viewport)
    viewport.addEventListener("pointerdown", onSelect, true)
    viewport.addEventListener("focusin", onSelect)
    viewport.addEventListener("keydown", onKeyDown, true)
    viewport.addEventListener("scroll", onScroll, { passive: true })
    return () => {
      observer.disconnect()
      viewport.removeEventListener("pointerdown", onSelect, true)
      viewport.removeEventListener("focusin", onSelect)
      viewport.removeEventListener("keydown", onKeyDown, true)
      viewport.removeEventListener("scroll", onScroll)
    }
  }, [])
  return ref
}
