// Spec import dialog — paste Markdown → preview parsed pieces → confirm.
// Preview is read-only; if the H2/H3 split is wrong the user fixes the
// markdown and re-previews.

import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { Button } from "../../components/ui/button"
import { ApiError } from "../../lib/api-client"
import {
  useConfirmSpecImportMutation,
  usePreviewSpecImportMutation,
  type ImportSpecPiece,
} from "../../lib/api-specs"

interface SpecImportDialogProps {
  workspaceID: string
  onClose: () => void
}

type Stage =
  | { kind: "edit" }
  | { kind: "preview"; pieces: ImportSpecPiece[]; text: string }

export function SpecImportDialog({ workspaceID, onClose }: SpecImportDialogProps) {
  const { t } = useTranslation("admin")
  const [text, setText] = useState("")
  const [stage, setStage] = useState<Stage>({ kind: "edit" })
  const previewMut = usePreviewSpecImportMutation(workspaceID)
  const confirmMut = useConfirmSpecImportMutation(workspaceID)

  const previewErr = previewMut.error as ApiError | undefined
  const confirmErr = confirmMut.error as ApiError | undefined
  const busy = previewMut.isPending || confirmMut.isPending

  const handlePreview = async () => {
    const trimmed = text.trim()
    if (!trimmed) return
    try {
      const res = await previewMut.mutateAsync(trimmed)
      setStage({ kind: "preview", pieces: res.pieces, text: trimmed })
    } catch {
      /* error rendered inline */
    }
  }

  const handleConfirm = async () => {
    if (stage.kind !== "preview") return
    try {
      await confirmMut.mutateAsync(stage.text)
      onClose()
    } catch {
      /* error rendered inline */
    }
  }

  const handleBack = () => {
    setStage({ kind: "edit" })
    confirmMut.reset()
  }

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !busy) onClose() }}>
      <DialogContent className="max-w-3xl gap-0 p-0">
        <DialogHeader className="border-b border-slate-100 px-5 py-4 pr-10">
          <DialogTitle className="text-sm">{t("specs.import.title")}</DialogTitle>
          <DialogDescription>{t("specs.import.description")}</DialogDescription>
        </DialogHeader>

        {stage.kind === "edit" ? (
          <div className="space-y-3 px-5 py-4">
            <label className="block space-y-1">
              <span className="text-[12px] font-medium text-slate-700">
                {t("specs.import.field.text")}
              </span>
              <textarea
                value={text}
                onChange={(event) => setText(event.target.value)}
                placeholder={t("specs.import.placeholder.text")}
                rows={18}
                className="block w-full rounded-md border border-slate-200 px-3 py-2 font-mono text-[12.5px] leading-relaxed text-slate-800 placeholder:text-slate-400 focus:border-slate-400 focus:outline-none focus:ring-1 focus:ring-slate-300"
              />
            </label>
            {previewErr && (
              <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2">
                <p className="text-[12px] font-medium text-red-900">
                  {t("specs.import.error.previewTitle")}
                </p>
                <p className="text-[11.5px] text-red-700">{previewErr.message}</p>
              </div>
            )}
          </div>
        ) : (
          <div className="space-y-3 px-5 py-4">
            <p className="text-[12.5px] font-medium text-slate-700">
              {t("specs.import.preview.title", { count: stage.pieces.length })}
            </p>
            {stage.pieces.length === 0 ? (
              <div className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-[12px] text-amber-800">
                {t("specs.import.preview.empty")}
              </div>
            ) : (
              <ul className="max-h-[420px] space-y-2 overflow-y-auto rounded-md border border-slate-200 bg-slate-50 p-3">
                {stage.pieces.map((piece, idx) => (
                  <li
                    key={`${piece.title}-${idx}`}
                    className="rounded-md border border-slate-200 bg-white px-3 py-2"
                  >
                    <p className="text-[13px] font-semibold text-slate-900">{piece.title}</p>
                    <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-[11.5px] leading-relaxed text-slate-600">
                      {piece.body}
                    </pre>
                  </li>
                ))}
              </ul>
            )}
            {confirmErr && (
              <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2">
                <p className="text-[12px] font-medium text-red-900">
                  {t("specs.import.error.confirmTitle")}
                </p>
                <p className="text-[11.5px] text-red-700">{confirmErr.message}</p>
              </div>
            )}
          </div>
        )}

        <DialogFooter className="flex flex-row items-center justify-end gap-2 border-t border-slate-100 bg-slate-50/60 px-4 py-3">
          {stage.kind === "edit" ? (
            <>
              <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={busy}>
                {t("specs.import.actions.cancel")}
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={handlePreview}
                disabled={busy || text.trim().length === 0}
              >
                {previewMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {t("specs.import.actions.preview")}
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="outline" size="sm" onClick={handleBack} disabled={busy}>
                {t("specs.import.actions.back")}
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={handleConfirm}
                disabled={busy || stage.pieces.length === 0}
              >
                {confirmMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {t("specs.import.actions.confirm")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
