/**
 * Inline-create form for credential_kinds. Sits over the import dialog
 * so encountering an unregistered vendor (e.g. Linear) doesn't break
 * the in-progress import. Only minimal fields exposed — value_schema is
 * accepted by the API but not rendered here.
 */
import { useEffect, useState } from "react"
import { Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { ApiError } from "../../../lib/api-client"

import { useCreateCredentialKindMutation } from "./api"
import type { CredentialKindRead } from "./types"

interface Props {
  workspaceID: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Called after a successful create — the EnvCredentialPicker uses this to
   *  flip the row to the newly-created kind. */
  onCreated: (kind: CredentialKindRead) => void
  /** When the trigger had a partial code typed (e.g. user typed "lin" in the
   *  combobox search before clicking "+ new"), pre-fill that here. */
  initialCode?: string
}

export function NewCredentialKindInlineDialog({
  workspaceID,
  open,
  onOpenChange,
  onCreated,
  initialCode,
}: Props) {
  const { t } = useTranslation("admin")
  const mut = useCreateCredentialKindMutation(workspaceID)

  const [code, setCode] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [description, setDescription] = useState("")

  // Seed only on the open transition — subsequent edits stay user-driven.
  useEffect(() => {
    if (!open) return
    setCode(initialCode?.toLowerCase() ?? "")
    setDisplayName("")
    setDescription("")
    mut.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const codeTrimmed = code.trim().toLowerCase()
  const isValidCode = /^[a-z][a-z0-9_]*$/.test(codeTrimmed)
  const errMsg = mut.error instanceof ApiError
    ? mut.error.envelope.message
    : mut.error instanceof Error
      ? mut.error.message
      : null
  const disabled = mut.isPending || !isValidCode || displayName.trim().length === 0

  const submit = () => {
    if (disabled) return
    mut.mutate(
      {
        code: codeTrimmed,
        display_name: displayName.trim(),
        description: description.trim() || undefined,
      },
      {
        onSuccess: (kind) => {
          onCreated(kind)
          onOpenChange(false)
        },
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("capabilities.import.newKind.title", "新建凭据类型")}</DialogTitle>
        </DialogHeader>

        <div className="space-y-3">
          <Field
            label={t("capabilities.import.newKind.code.label", "Code")}
            help={t(
              "capabilities.import.newKind.code.help",
              "小写字母 + 数字 + 下划线,如 linear_api_key",
            )}
            required
          >
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="linear_api_key"
              autoFocus
            />
            {code.trim().length > 0 && !isValidCode && (
              <p className="text-[11px] text-red-600">
                {t(
                  "capabilities.import.newKind.code.invalid",
                  "code 必须以小写字母开头,只能包含小写字母、数字、下划线",
                )}
              </p>
            )}
          </Field>

          <Field
            label={t("capabilities.import.newKind.displayName.label", "显示名")}
            required
          >
            <Input
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Linear API Key"
            />
          </Field>

          <Field label={t("capabilities.import.newKind.description.label", "说明")}>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t(
                "capabilities.import.newKind.description.placeholder",
                "可选,告诉用户去哪里获取这个 token",
              )}
            />
          </Field>

          {errMsg && (
            <div
              role="alert"
              className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-800"
            >
              {errMsg}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            disabled={mut.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("capabilities.actions.cancel")}
          </Button>
          <Button size="sm" disabled={disabled} onClick={submit}>
            {mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("capabilities.import.newKind.submit", "新建")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Field({
  label,
  help,
  required,
  children,
}: {
  label: string
  help?: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="grid gap-1.5">
      <span className="text-[12px] font-medium text-slate-700">
        {label}
        {required && <span className="text-red-500"> *</span>}
      </span>
      {children}
      {help && <span className="text-[11px] leading-relaxed text-slate-500">{help}</span>}
    </label>
  )
}
