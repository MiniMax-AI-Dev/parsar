import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react"

/**
 * A draggable panel width with three persistence outcomes.
 *
 * After a drag the width is "dirty": the caller shows a prompt with
 * save / temporary / restore. `save` writes localStorage (survives reload),
 * `keepTemporary` writes sessionStorage (this tab only), `restore` springs
 * back to the default and clears both. Resolution order on load:
 * localStorage → sessionStorage → default.
 */
export interface ResizableWidth {
  width: number
  dragging: boolean
  /** True while the width is animating back to its default. */
  restoring: boolean
  /** True after a drag until the user picks save / temporary / restore. */
  dirty: boolean
  /** Attach to the resize handle. */
  handleProps: {
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) => void
    onKeyDown: (e: React.KeyboardEvent<HTMLElement>) => void
  }
  save: () => void
  keepTemporary: () => void
  restore: () => void
}

interface Options {
  storageKey: string
  defaultWidth: number
  min: number
  max: number
  /** "right" when the handle is on the panel's right edge (sidebar); "left" for a rail. */
  edge: "left" | "right"
}

const PREFIX = "parsar.layout."

function readStored(key: string, min: number, max: number): number | null {
  if (typeof window === "undefined") return null
  for (const store of [window.localStorage, window.sessionStorage]) {
    try {
      const raw = store.getItem(PREFIX + key)
      const n = raw ? Number(raw) : NaN
      if (Number.isFinite(n)) return Math.min(max, Math.max(min, n))
    } catch {
      /* ignore */
    }
  }
  return null
}

export function useResizableWidth({ storageKey, defaultWidth, min, max, edge }: Options): ResizableWidth {
  const [width, setWidth] = useState(() => readStored(storageKey, min, max) ?? defaultWidth)
  const [dragging, setDragging] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const [dirty, setDirty] = useState(false)
  const startRef = useRef<{ x: number; width: number } | null>(null)
  const widthRef = useRef(width)
  useEffect(() => {
    widthRef.current = width
  }, [width])

  const clamp = useCallback((n: number) => Math.min(max, Math.max(min, Math.round(n))), [min, max])

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLElement>) => {
      if (e.button !== 0) return
      e.preventDefault()
      startRef.current = { x: e.clientX, width: widthRef.current }
      setDragging(true)
      setRestoring(false)
      const target = e.currentTarget
      target.setPointerCapture?.(e.pointerId)
      const move = (ev: PointerEvent) => {
        if (!startRef.current) return
        const dx = ev.clientX - startRef.current.x
        setWidth(clamp(startRef.current.width + (edge === "right" ? dx : -dx)))
      }
      const up = () => {
        const moved = startRef.current && Math.abs(widthRef.current - startRef.current.width) >= 2
        startRef.current = null
        setDragging(false)
        if (moved) setDirty(true)
        window.removeEventListener("pointermove", move)
        window.removeEventListener("pointerup", up)
        window.removeEventListener("pointercancel", up)
      }
      window.addEventListener("pointermove", move)
      window.addEventListener("pointerup", up)
      window.addEventListener("pointercancel", up)
    },
    [clamp, edge],
  )

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLElement>) => {
      const step = e.shiftKey ? 32 : 8
      const dir = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0
      if (!dir) return
      e.preventDefault()
      setWidth((w) => clamp(w + (edge === "right" ? dir : -dir) * step))
      setDirty(true)
    },
    [clamp, edge],
  )

  const save = useCallback(() => {
    try {
      window.localStorage.setItem(PREFIX + storageKey, String(widthRef.current))
      window.sessionStorage.removeItem(PREFIX + storageKey)
    } catch {
      /* ignore */
    }
    setDirty(false)
  }, [storageKey])

  const keepTemporary = useCallback(() => {
    try {
      window.sessionStorage.setItem(PREFIX + storageKey, String(widthRef.current))
      window.localStorage.removeItem(PREFIX + storageKey)
    } catch {
      /* ignore */
    }
    setDirty(false)
  }, [storageKey])

  const restore = useCallback(() => {
    try {
      window.localStorage.removeItem(PREFIX + storageKey)
      window.sessionStorage.removeItem(PREFIX + storageKey)
    } catch {
      /* ignore */
    }
    setRestoring(true)
    setWidth(defaultWidth)
    setDirty(false)
  }, [storageKey, defaultWidth])

  // The spring-back transition only lives for the duration of a restore.
  useEffect(() => {
    if (!restoring) return
    const id = window.setTimeout(() => setRestoring(false), 420)
    return () => window.clearTimeout(id)
  }, [restoring])

  return { width, dragging, restoring, dirty, handleProps: { onPointerDown, onKeyDown }, save, keepTemporary, restore }
}
