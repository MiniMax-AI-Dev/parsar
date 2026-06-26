/**
 * Drag/drop + click-to-browse picker for a Skill .zip. Uses
 * react-dropzone for keyboard/ARIA wiring and flicker-free dragover.
 * Callers run the upload chain.
 */
import { useTranslation } from "react-i18next"
import { useDropzone, type FileRejection } from "react-dropzone"
import { FileArchive, Loader2, Upload, X } from "lucide-react"

import { Button } from "../../../components/ui/button"

const ACCEPTED_MIME = {
  "application/zip": [".zip"],
  "application/x-zip-compressed": [".zip"],
  "application/octet-stream": [".zip"],
}
const MAX_BYTES = 8 * 1024 * 1024 // mirror planned server-side cap for skill zips

interface Props {
  file: File | null
  /** Truthy = busy spinner inside the file card. */
  busy?: boolean
  /** Text under the spinner (e.g. "上传中", "解析中"). */
  busyLabel?: string
  /** Called with the picked file. Caller runs the upload chain. */
  onPick: (file: File) => void
  /** Clear the current selection. */
  onClear: () => void
  /** Local-validation error to show beneath the picker (e.g. "请选择 .zip"). */
  localError?: string | null
}

export function SkillZipDropzone({
  file,
  busy,
  busyLabel,
  onPick,
  onClear,
  localError,
}: Props) {
  const { t } = useTranslation("admin")

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    accept: ACCEPTED_MIME,
    maxSize: MAX_BYTES,
    maxFiles: 1,
    multiple: false,
    onDrop: (accepted: File[]) => {
      const picked = accepted[0]
      if (picked) onPick(picked)
    },
    onDropRejected: (rejections: FileRejection[]) => {
      // Parent re-validates and surfaces messages via localError.
      void rejections
    },
    // Disable while busy so a second drop can't race the first chain.
    disabled: !!busy,
  })

  if (!file) {
    return (
      <div className="grid gap-2">
        <div
          {...getRootProps()}
          className={`flex cursor-pointer flex-col items-center justify-center gap-2 rounded-md border-2 border-dashed px-4 py-8 text-center transition-colors ${
            isDragActive
              ? "border-slate-500 bg-slate-100"
              : "border-slate-300 bg-slate-50 hover:border-slate-400"
          }`}
        >
          <input {...getInputProps()} />
          <Upload className="h-5 w-5 text-slate-500" />
          <span className="text-[13px] text-slate-700">
            {isDragActive
              ? t("capabilities.import.skill.dropActive", "松开导入 .zip")
              : t("capabilities.import.skill.dropHint", "拖拽或点击上传 .zip(SKILL.md + references / scripts)")}
          </span>
          <span className="text-[11px] text-slate-500">
            {t("capabilities.import.skill.sizeHint", "最大 8 MiB")}
          </span>
        </div>
        {localError && (
          <div
            role="alert"
            className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-800"
          >
            {localError}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-3 rounded-md border border-slate-200 bg-white px-3 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <FileArchive className="h-4 w-4 shrink-0 text-slate-500" />
          <div className="min-w-0">
            <p className="truncate text-[13px] text-slate-900">{file.name}</p>
            <p className="text-[11px] text-slate-500">
              {formatBytes(file.size)}
              {busy && (
                <>
                  {" · "}
                  <span className="inline-flex items-center gap-1 text-slate-600">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {busyLabel ?? t("capabilities.import.skill.uploading", "上传中…")}
                  </span>
                </>
              )}
            </p>
          </div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={onClear}
          disabled={busy}
          aria-label={t("capabilities.actions.cancel", "取消")}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
      {localError && (
        <div
          role="alert"
          className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-800"
        >
          {localError}
        </div>
      )}
    </div>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}
