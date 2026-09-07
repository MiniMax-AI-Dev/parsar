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
import { Label } from "../../../components/ui/label"
import { ApiError } from "../../../lib/api-client"
import type { Agent } from "../../../lib/api-types"
import { InlineError } from "./DetailSection"

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
        <div className="flex flex-col gap-3">
          <div>
            <Label htmlFor="delete-agent-confirmation" className="flex flex-wrap items-center gap-1.5">
              <span>{t("agents.delete.confirmNamePrefix")}</span>
              <code className="font-mono text-xs text-fg">{expected}</code>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                disabled={!expected || pending}
                aria-label={t("agents.delete.copyName")}
                onClick={() => void copyAgentName()}
              >
                {copied ? <Check strokeWidth={1.5} aria-hidden="true" /> : <Copy strokeWidth={1.5} aria-hidden="true" />}
              </Button>
              <span>{t("agents.delete.confirmNameSuffix")}</span>
            </Label>
            <Input
              id="delete-agent-confirmation"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              disabled={pending}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          {msg && <InlineError>{msg}</InlineError>}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button
              variant="outline"
              disabled={pending}
              onClick={() => {
                setConfirmation("")
                onCancel()
              }}
            >
              {t("agents.listActions.cancel")}
            </Button>
          </AlertDialogCancel>
          <Button variant="destructive" disabled={!canDelete} onClick={onConfirm}>
            {pending && <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />}
            {t("agents.delete.confirm")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
