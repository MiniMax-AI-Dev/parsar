import { Check, Copy, Loader2 } from "lucide-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { ApiError } from "../../../lib/api-client"
import type { Agent } from "../../../lib/api-types"

function errorMessage(error: unknown): string | null {
  if (!error) return null
  if (error instanceof ApiError) return error.envelope.message || error.message
  if (error instanceof Error) return error.message
  return String(error)
}

export function DeleteAgentDialog({
  agent,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  agent: Agent | null
  pending: boolean
  error: unknown
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  const [confirmation, setConfirmation] = useState("")
  const [copied, setCopied] = useState(false)
  const expected = agent?.name ?? ""
  const canDelete = Boolean(agent) && confirmation === expected && !pending
  const msg = errorMessage(error)

  useEffect(() => {
    setConfirmation("")
    setCopied(false)
  }, [agent?.id])

  async function copyAgentName() {
    if (!expected || pending) return
    try {
      await navigator.clipboard?.writeText(expected)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1200)
    } catch {
      setCopied(false)
    }
  }

  return (
    <AlertDialog
      open={agent !== null}
      onOpenChange={(open) => {
        if (!open && !pending) {
          setConfirmation("")
          onCancel()
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("agents.delete.title", { name: expected })}</AlertDialogTitle>
          <AlertDialogDescription>{t("agents.delete.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-2">
          <label className="flex flex-wrap items-center gap-1.5 text-sm font-medium text-fg" htmlFor="delete-agent-confirmation">
            <span>{t("agents.delete.confirmNamePrefix")}</span>
            <span className="rounded border border-danger-border bg-danger-subtle px-1.5 py-0.5 font-mono text-xs font-semibold text-danger-emphasis">
              {expected}
            </span>
            <button
              type="button"
              className="inline-flex h-7 w-7 items-center justify-center rounded-md text-fg-subtle transition-colors hover:bg-surface-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong disabled:cursor-not-allowed disabled:opacity-50"
              disabled={!expected || pending}
              title={t("agents.delete.copyName")}
              aria-label={t("agents.delete.copyName")}
              onClick={() => void copyAgentName()}
            >
              {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
            </button>
            <span>{t("agents.delete.confirmNameSuffix")}</span>
          </label>
          <Input
            id="delete-agent-confirmation"
            value={confirmation}
            onChange={(event) => setConfirmation(event.target.value)}
            disabled={pending}
            autoComplete="off"
            spellCheck={false}
          />
          {msg && <p className="text-sm text-danger">{msg}</p>}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button
              variant="outline"
              size="sm"
              disabled={pending}
              onClick={() => {
                setConfirmation("")
                onCancel()
              }}
            >
              {t("agents.listActions.cancel")}
            </Button>
          </AlertDialogCancel>
          <Button variant="destructive" size="sm" disabled={!canDelete} onClick={onConfirm}>
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("agents.delete.confirm")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
