import { useState, type RefObject } from "react"
import { ErrorDialog } from "../../components/ui/error-dialog"
import { ApiError } from "../../lib/api-client"

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) return err.envelope.message || err.message
  if (err instanceof Error) return err.message
  return String(err)
}

export function ModelSaveErrorDialog({ error, title, formRef }: {
  error: unknown
  title: string
  formRef: RefObject<HTMLFormElement | null>
}) {
  const [dismissedError, setDismissedError] = useState<unknown>(null)
  const message = extractErrorMessage(error)
  if (!message || error === dismissedError) return null

  return <ErrorDialog
    title={title}
    message={message}
    onClose={() => setDismissedError(error)}
    onRestoreFocus={() => formRef.current?.querySelector<HTMLButtonElement>('button[type="submit"]')?.focus()}
    focusScopeRef={formRef}
  />
}
