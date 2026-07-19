/**
 * Plugin tab. Browser PUTs the zip directly to OSS via a presigned URL;
 * preview + commit both reference the ossKey so the server re-fetches
 * the same bytes (server is the SHA-256 oracle — preview running on the
 * browser's copy while commit runs on OSS would let a slow uploader
 * "preview a good plugin, ship a different one").
 */
import { useEffect, useRef, useState } from "react"
import { Loader2, Upload, FileArchive, X } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"

import {
  uploadPluginZipDirect,
  useImportPreviewMutation,
  usePresignUploadMutation,
} from "./api"
import type {
  CanonicalSpec,
  ImportPreviewResponse,
  PluginValidationResult,
} from "./types"

interface Props {
  workspaceID: string | null
  onChange: (spec: CanonicalSpec | null) => void
  /** Called with the ossKey when upload + preview succeed; the dialog
   *  attaches it to the commit body so the server can re-fetch and
   *  rebuild the spec. */
  onUploadStateChange: (state: PluginUploadState) => void
  onSuggestedName?: (name: string) => void
}

/** Resolved state the parent uses to assemble the commit payload. */
export interface PluginUploadState {
  ossKey: string | null
  uploadSource: "zip" | null
  /** Most recent validation result so the parent dialog can render
   *  blocking errors before the user hits Commit. */
  validation: PluginValidationResult | null
}

const ACCEPTED_MIME = "application/zip,application/x-zip-compressed,application/octet-stream"
const ACCEPTED_EXT = ".zip"
const MAX_BYTES = 32 * 1024 * 1024 // mirror server-side cap

export function ImportPluginForm({
  workspaceID,
  onChange,
  onUploadStateChange,
  onSuggestedName,
}: Props) {
  const { t } = useTranslation("admin")
  const presignMut = usePresignUploadMutation(workspaceID)
  const previewMut = useImportPreviewMutation(workspaceID)

  const [file, setFile] = useState<File | null>(null)
  const [ossKey, setOssKey] = useState<string | null>(null)
  const [validation, setValidation] = useState<PluginValidationResult | null>(null)
  const [localErr, setLocalErr] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  // Race guard: if the user picks a second file while the first chain is
  // in-flight, a slower in-flight chain could overwrite ossKey/validation
  // with stale values. Each acceptFile bumps the counter; awaits bail if
  // they no longer match.
  const requestSeq = useRef(0)

  // Push upload state up so the dialog can disable submit on
  // validation.valid === false.
  useEffect(() => {
    onUploadStateChange({
      ossKey,
      uploadSource: ossKey ? "zip" : null,
      validation,
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ossKey, validation])

  const reset = () => {
    requestSeq.current++ // invalidate any in-flight chain
    setFile(null)
    setOssKey(null)
    setValidation(null)
    setLocalErr(null)
    onChange(null)
    presignMut.reset()
    previewMut.reset()
    if (inputRef.current) inputRef.current.value = ""
  }

  const acceptFile = async (picked: File) => {
    setLocalErr(null)
    if (!picked.name.toLowerCase().endsWith(ACCEPTED_EXT)) {
      setLocalErr(t("capabilities.import.plugin.errors.notZip", "Please choose a .zip file"))
      return
    }
    if (picked.size > MAX_BYTES) {
      setLocalErr(
        t("capabilities.import.plugin.errors.tooLarge", "File exceeds 32 MiB — the server will reject it"),
      )
      return
    }
    // Stamp this call as the current one — any earlier in-flight call
    // will see myReq !== requestSeq.current and bail out before
    // touching state.
    const myReq = ++requestSeq.current
    setFile(picked)
    setValidation(null)
    setOssKey(null)
    onChange(null)

    try {
      const presign = await presignMut.mutateAsync({
        filename: picked.name,
        prefix: "plugin",
      })
      if (myReq !== requestSeq.current) return
      await uploadPluginZipDirect(presign, picked)
      if (myReq !== requestSeq.current) return
      const preview = await previewMut.mutateAsync({
        kind: "plugin",
        oss_key: presign.ossKey,
        upload_source: "zip",
      })
      if (myReq !== requestSeq.current) return
      setOssKey(presign.ossKey)
      setValidation(preview.plugin_validation ?? null)
      // Only hand the spec up if validation actually passed; the
      // preview response carries an empty spec when valid=false.
      if ((preview.plugin_validation?.valid ?? true) && preview.canonical_spec.kind === "plugin") {
        onChange(preview.canonical_spec)
        if (preview.suggested_name) onSuggestedName?.(preview.suggested_name)
      } else {
        onChange(null)
      }
    } catch (err) {
      if (myReq !== requestSeq.current) return
      if (err instanceof ApiError) {
        setLocalErr(err.envelope.message)
      } else if (err instanceof Error) {
        setLocalErr(err.message)
      } else {
        setLocalErr(String(err))
      }
    }
  }

  const onDrop = (e: React.DragEvent<HTMLLabelElement>) => {
    e.preventDefault()
    const picked = e.dataTransfer.files?.[0]
    if (picked) void acceptFile(picked)
  }

  const onDragOver = (e: React.DragEvent<HTMLLabelElement>) => {
    e.preventDefault()
  }

  const busy = presignMut.isPending || previewMut.isPending
  const errMsg = localErr ?? null

  return (
    <div className="grid gap-4 md:grid-cols-2">
      <div className="grid gap-3">
        <span className="text-sm font-medium text-fg-muted">
          {t("capabilities.import.plugin.uploadLabel", "Upload Plugin zip")}
        </span>
        {!file ? (
          <label
            htmlFor="plugin-zip-input"
            onDrop={onDrop}
            onDragOver={onDragOver}
            className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-md border-2 border-dashed border-line-strong bg-surface-subtle px-4 py-8 text-center hover:border-line-strong"
          >
            <Upload className="h-5 w-5 text-fg-subtle" />
            <span className="text-sm text-fg-muted">
              {t("capabilities.import.plugin.dropHint", "Drag or click to upload a .zip file")}
            </span>
            <span className="text-xs text-fg-subtle">
              {t("capabilities.import.plugin.sizeHint", "Up to 32 MiB")}
            </span>
            <input
              id="plugin-zip-input"
              ref={inputRef}
              type="file"
              accept={ACCEPTED_MIME + "," + ACCEPTED_EXT}
              className="hidden"
              onChange={(e) => {
                const picked = e.target.files?.[0]
                if (picked) void acceptFile(picked)
              }}
            />
          </label>
        ) : (
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
                        {presignMut.isPending
                          ? t("capabilities.import.plugin.uploading", "Uploading…")
                          : t("capabilities.import.plugin.validating", "Validating…")}
                      </span>
                    </>
                  )}
                </p>
              </div>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={reset}
              disabled={busy}
              aria-label={t("capabilities.actions.cancel", "Cancel")}
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        )}

        {errMsg && (
          <div
            role="alert"
            className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
          >
            {errMsg}
          </div>
        )}
      </div>

      {/* ---- validation preview pane ----------------------------------- */}
      <div className="grid gap-3">
        <span className="text-sm font-medium text-fg-muted">
          {t("capabilities.import.plugin.previewLabel", "Validation result")}
        </span>
        {!validation && !busy && (
          <div className="rounded-md border border-dashed border-line-strong bg-surface-subtle px-3 py-6 text-center text-sm text-fg-subtle">
            {t("capabilities.import.plugin.previewEmpty", "Upload a zip file to see the validation result here")}
          </div>
        )}
        {validation && <ValidationPanel validation={validation} preview={previewMut.data ?? null} />}
      </div>
    </div>
  )
}

function ValidationPanel({
  validation,
  preview,
}: {
  validation: PluginValidationResult
  preview: ImportPreviewResponse | null
}) {
  const { t } = useTranslation("admin")
  const errors = validation.errors ?? []
  const warnings = validation.warnings ?? []
  // Fall back to validation.manifest so a failed parse still shows
  // which plugin failed (name/version), not a banner alone.
  const manifestFromSpec = preview?.canonical_spec.plugin
  const manifestFromValidation = validation.manifest
  const m = manifestFromSpec
    ? {
        name: manifestFromSpec.name,
        version: manifestFromSpec.version,
        description: manifestFromSpec.description,
        author: manifestFromSpec.author,
      }
    : manifestFromValidation
    ? {
        name: manifestFromValidation.name ?? "",
        version: manifestFromValidation.version ?? "",
        description: manifestFromValidation.description,
        author: manifestFromValidation.author?.name,
      }
    : null

  return (
    <div className="grid gap-3 rounded-md border border-line bg-surface p-3">
      {validation.valid ? (
        <div className="rounded-md border border-success-border bg-success-subtle px-3 py-1.5 text-sm text-success-emphasis">
          {t("capabilities.import.plugin.passed", "Validation passed")}
        </div>
      ) : (
        <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-1.5 text-sm text-danger-emphasis">
          {t("capabilities.import.plugin.failed", "Validation failed — fix the issues and upload again")}
        </div>
      )}

      {m && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
          <dt className="text-fg-subtle">name</dt>
          <dd className="font-mono text-fg">{m.name}</dd>
          <dt className="text-fg-subtle">version</dt>
          <dd className="font-mono text-fg">{m.version}</dd>
          {m.description && (
            <>
              <dt className="text-fg-subtle">description</dt>
              <dd className="text-fg-muted">{m.description}</dd>
            </>
          )}
          {m.author && (
            <>
              <dt className="text-fg-subtle">author</dt>
              <dd className="text-fg-muted">{m.author}</dd>
            </>
          )}
        </dl>
      )}

      {errors.length > 0 && (
        <div>
          <p className="mb-1 text-xs font-medium uppercase tracking-wide text-danger-emphasis">
            {t("capabilities.import.plugin.errorsHeader", "Errors")}
          </p>
          <ul className="ml-4 list-disc space-y-0.5 text-sm text-danger-emphasis">
            {errors.map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        </div>
      )}

      {warnings.length > 0 && (
        <div>
          <p className="mb-1 text-xs font-medium uppercase tracking-wide text-warning">
            {t("capabilities.import.plugin.warningsHeader", "Warnings")}
          </p>
          <ul className="ml-4 list-disc space-y-0.5 text-sm text-warning-emphasis">
            {warnings.map((wn, i) => (
              <li key={i}>{wn}</li>
            ))}
          </ul>
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
