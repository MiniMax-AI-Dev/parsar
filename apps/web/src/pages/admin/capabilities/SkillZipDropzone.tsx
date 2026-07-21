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
  /** Text under the spinner (e.g. "Uploading", "Parsing"). */
  busyLabel?: string
  /** Called with the picked file. Caller runs the upload chain. */
  onPick: (file: File) => void
  /** Clear the current selection. */
  onClear: () => void
  /** Local-validation error to show beneath the picker (e.g. "Please select a .zip"). */
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
              ? "border-line-strong bg-surface-muted"
              : "border-line-strong bg-surface-subtle hover:border-line-strong"
          }`}
        >
          <input {...getInputProps()} />
          <Upload className="h-5 w-5 text-fg-subtle" />
          <span className="text-sm text-fg-muted">
            {isDragActive
              ? t("capabilities.import.skill.dropActive", "Release to import the .zip")
              : t("capabilities.import.skill.dropHint", "Drag or click to upload a Skill .zip")}
          </span>
          <span className="text-xs text-fg-subtle">
            {t("capabilities.import.skill.sizeHint", "Up to 8 MiB")}
          </span>
        </div>
        {localError && (
          <div
            role="alert"
            className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
          >
            {localError}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-3 rounded-md border border-line bg-surface px-3 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <FileArchive className="h-4 w-4 shrink-0 text-fg-subtle" />
          <div className="min-w-0">
            <p className="truncate text-sm text-fg">{file.name}</p>
            <p className="text-xs text-fg-subtle">
              {formatBytes(file.size)}
              {busy && (
                <>
                  {" · "}
                  <span className="inline-flex items-center gap-1 text-fg-muted">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {busyLabel ?? t("capabilities.import.skill.uploading", "Uploading…")}
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
          aria-label={t("capabilities.actions.cancel", "Cancel")}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
      {localError && (
        <div
          role="alert"
          className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
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
