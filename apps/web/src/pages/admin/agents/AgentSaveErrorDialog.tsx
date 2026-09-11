import { useState, type RefObject } from "react"
import { ErrorDialog } from "../../../components/ui/error-dialog"

export function AgentSaveErrorDialog({ error, message, title, submitRef, scopeRef }: {
  error: unknown
  message: string | null
  title: string
  submitRef: RefObject<HTMLButtonElement | null>
  scopeRef: RefObject<HTMLDivElement | null>
}) {
  const [dismissedError, setDismissedError] = useState<unknown>(null)
  if (!message || error === dismissedError) return null

  return <ErrorDialog
    title={title}
    message={message}
    onClose={() => setDismissedError(error)}
    onRestoreFocus={() => submitRef.current?.focus()}
    focusScopeRef={scopeRef}
  />
}
