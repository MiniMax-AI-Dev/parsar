import { useRef, type FocusEvent } from "react"

export function useWorkspaceDialogFocus(dialogOpen: boolean) {
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const originRef = useRef<HTMLElement | null>(null)

  function rememberFocus(event: FocusEvent<HTMLDivElement>) {
    originRef.current = event.target
  }

  function restoreFocus(event: Event) {
    event.preventDefault()
    // A closing discovery dialog may be handing focus to a join dialog.
    if (dialogOpen) return
    const origin = originRef.current
    if (origin?.isConnected) origin.focus()
    else (menuRef.current ?? triggerRef.current)?.focus()
  }

  return { triggerRef, menuRef, rememberFocus, restoreFocus }
}
