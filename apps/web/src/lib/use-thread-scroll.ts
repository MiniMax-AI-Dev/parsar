import { useCallback, useLayoutEffect, useRef, useState } from "react"

/** Follow new output until the reader scrolls back or selects an earlier turn. */
export function useThreadScroll(
  conversationId: string,
  viewport: HTMLElement | null,
  content: HTMLElement | null,
) {
  const following = useRef(true)
  const activeConversation = useRef<string | null>(null)
  const previousTop = useRef(0)
  const [showScrollToLatest, setShowScrollToLatest] = useState(false)

  const scrollToLatest = useCallback(() => {
    if (activeConversation.current !== conversationId) return
    following.current = true
    if (!viewport) return
    viewport.scrollTo({ top: viewport.scrollHeight, behavior: "instant" })
    previousTop.current = viewport.scrollTop
    setShowScrollToLatest(false)
  }, [conversationId, viewport])

  const scrollToTurn = useCallback((key: string) => {
    if (!viewport) return
    const turn = viewport.querySelector<HTMLElement>(`[data-turn-key="${CSS.escape(key)}"]`)
    if (!turn) return
    following.current = false
    turn.scrollIntoView({
      block: "start",
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth",
    })
  }, [viewport])

  useLayoutEffect(() => {
    if (!viewport || !content) return
    activeConversation.current = conversationId
    following.current = true
    previousTop.current = viewport.scrollTop
    const atBottom = () => viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop <= 2
    const onScroll = () => {
      const bottom = atBottom()
      if (viewport.scrollTop < previousTop.current) following.current = false
      // A smooth jump can start with a tiny upward step still near the bottom.
      // Only movement back down to the bottom resumes following.
      else if (bottom && viewport.scrollTop > previousTop.current) following.current = true
      previousTop.current = viewport.scrollTop
      setShowScrollToLatest(!bottom)
    }
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) following.current = false
    }
    const onResize = () => {
      // Content growth is not a reader scrolling away from the latest turn.
      if (following.current) scrollToLatest()
      else setShowScrollToLatest(!atBottom())
    }
    const frame = window.requestAnimationFrame(scrollToLatest)
    const observer = new ResizeObserver(onResize)
    observer.observe(viewport)
    observer.observe(content)
    viewport.addEventListener("scroll", onScroll, { passive: true })
    viewport.addEventListener("wheel", onWheel, { passive: true })
    return () => {
      activeConversation.current = null
      window.cancelAnimationFrame(frame)
      observer.disconnect()
      viewport.removeEventListener("scroll", onScroll)
      viewport.removeEventListener("wheel", onWheel)
    }
  }, [conversationId, viewport, content, scrollToLatest])

  return { scrollToLatest, scrollToTurn, showScrollToLatest }
}
