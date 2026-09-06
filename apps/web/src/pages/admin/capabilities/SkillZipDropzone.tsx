/**
 * Drag/drop + click-to-browse picker for a Skill .zip. Uses
 * react-dropzone for keyboard/ARIA wiring and flicker-free dragover.
 * Callers run the upload chain.
 */
import { useTranslation } from "react-i18next"
import { useDropzone, type FileRejection } from "react-dropzone"
import { FileArchive, Loader2, Upload, X } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { cn } from "../../../lib/utils"
import { InlineNotice } from "./notices"

const ACCEPTED_MIME = {
  "application/zip": [".zip"],
  "application/x-zip-compressed": [".zip"],
  "application/octet-stream": [".zip"],
}
const MAX_BYTES = 8 * 1024 * 1024 // mirror planned server-side cap for skill zips

interface Props {
  file: File | null
  /** Truthy = busy spinner inside the file row. */
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
          className={cn(
            "flex cursor-pointer flex-col items-center justify-center gap-2 rounded-md border border-dashed border-line-strong px-4 py-8 text-center transition-colors duration-150 ease-settle focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40",
            isDragActive ? "app-pressed" : "bg-surface hover:app-hover",
          )}
        >
          <input {...getInputProps()} />
          <Upload className="h-4 w-4 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          <span className="text-sm text-fg">
            {isDragActive
              ? t("capabilities.import.skill.dropActive", "Release to import the .zip")
              : t("capabilities.import.skill.dropHint", "Drag or click to upload a Skill .zip")}
          </span>
          <span className="text-xs text-fg-muted">
            {t("capabilities.import.skill.sizeHint", "Up to 8 MiB")}
          </span>
        </div>
        {localError && <InlineNotice tone="error">{localError}</InlineNotice>}
      </div>
    )
  }

  return (
    <div className="grid gap-2">
      <div className="flex h-9 items-center gap-2 border-b border-line text-sm">
        <FileArchive className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-fg">{file.name}</span>
        <span className="shrink-0 font-mono text-xs tabular-nums text-fg-muted">{formatBytes(file.size)}</span>
        {busy && (
          <span className="inline-flex shrink-0 items-center gap-1 text-xs text-fg-muted">
            <Loader2 className="h-3 w-3 animate-spin" strokeWidth={1.5} aria-hidden="true" />
            {busyLabel ?? t("capabilities.import.skill.uploading", "Uploading…")}
          </span>
        )}
        <Button
          variant="ghost"
          size="icon"
          onClick={onClear}
          disabled={busy}
          aria-label={t("capabilities.actions.cancel", "Cancel")}
        >
          <X strokeWidth={1.5} />
        </Button>
      </div>
      {localError && <InlineNotice tone="error">{localError}</InlineNotice>}
    </div>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}
