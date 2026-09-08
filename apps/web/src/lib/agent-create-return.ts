import { useState, type MouseEvent } from "react"

export function replaceAgentModelSetup(event: MouseEvent<HTMLAnchorElement>) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  window.location.replace(event.currentTarget.href)
}

export function hasAgentCreateReturn(): boolean {
  return new URLSearchParams(window.location.search).get("return_to") === "agents.create"
}

export function useAgentCreateDialog() {
  const [open, setOpen] = useState(() =>
    hasAgentCreateReturn() && new URLSearchParams(window.location.search).get("focus") === "create",
  )

  function setCreateOpen(next: boolean) {
    setOpen(next)
    if (next || !hasAgentCreateReturn()) return
    const url = new URL(window.location.href)
    for (const key of ["focus", "return_to", "agent_name", "agent_description", "agent_prompt"]) {
      url.searchParams.delete(key)
    }
    window.history.replaceState(window.history.state, "", url.toString())
  }

  return [open, setCreateOpen] as const
}
