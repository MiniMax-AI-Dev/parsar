/**
 * Skill import: paste (single SKILL.md) or zip (multi-file). Both go
 * through /import/preview with different source_format. Server is the
 * parsing authority — preview/commit re-fetch from OSS so the client
 * never authors files[] directly. Preview pane is read-only: edits go
 * through re-pasting or re-zipping.
 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { ClipboardPaste, FileArchive } from "lucide-react"

import { Label } from "../../../components/ui/label"
import { Tabs, TabsList, TabsTrigger } from "../../../components/ui/tabs"
import { Textarea } from "../../../components/ui/textarea"
import { ApiError } from "../../../lib/api-client"
import {
  putToPresignedURL,
  useImportPreviewMutation,
  usePresignUploadMutation,
} from "./api"
import { ImportPreview } from "./ImportPreview"
import { SkillFileTree } from "./SkillFileTree"
import { SkillZipDropzone } from "./SkillZipDropzone"
import type { CanonicalSpec, SourceFormat } from "./types"

interface Props {
  workspaceID: string | null
  /** Current spec (initialized by parent from preview success). */
  value: CanonicalSpec | null
  onChange: (next: CanonicalSpec | null) => void
  /** Pre-fill parent's Name input from the parsed slug. */
  onSuggestedName: (name: string) => void
  /** Bubble up the description from the parsed frontmatter so the
   *  parent dialog can use it as capability.description verbatim
   *  (Skill imports treat frontmatter as the single source of truth). */
  onSuggestedDescription: (description: string) => void
  /** Bubble up the raw paste so the parent can stash it as source_payload. */
  onRawTextChange: (raw: string, format: SourceFormat) => void
  /**
   * Bubble up the ossKey of the uploaded zip so the parent's commit
   * payload can include it. Null when the user cleared the upload or
   * switched to paste mode.
   */
  onOssKeyChange: (ossKey: string | null) => void
  /**
   * Initial textarea content for the paste mode. Used by the "add new
   * version" dialog to seed the textarea from the previous version's
   * source_payload. Empty by default.
   */
  initialRawText?: string
}

type SourceMode = "paste" | "zip"

const ZIP_MAX_BYTES = 8 * 1024 * 1024

export function ImportSkillForm({
  workspaceID,
  value,
  onChange,
  onSuggestedName,
  onSuggestedDescription,
  onRawTextChange,
  onOssKeyChange,
  initialRawText,
}: Props) {
  const { t } = useTranslation("admin")
  const previewMut = useImportPreviewMutation(workspaceID)
  const presignMut = usePresignUploadMutation(workspaceID)

  // Default paste so single-SKILL.md users keep their existing UX.
  const [source, setSource] = useState<SourceMode>("paste")

  /* ---- paste mode state -------------------------------------------- */
  const [raw, setRaw] = useState(initialRawText ?? "")
  const [pasteWarnings, setPasteWarnings] = useState<string[]>([])
  const [pasteError, setPasteError] = useState<string | null>(null)
  const debounceRef = useRef<number | null>(null)

  /* ---- zip mode state ---------------------------------------------- */
  const [zipFile, setZipFile] = useState<File | null>(null)
  const [zipOssKey, setZipOssKey] = useState<string | null>(null)
  const [zipWarnings, setZipWarnings] = useState<string[]>([])
  const [zipError, setZipError] = useState<string | null>(null)
  // Race guard for picking a second zip mid-flight.
  const requestSeq = useRef(0)

  /* ---- paste debounced preview ------------------------------------- */
  useEffect(() => {
    if (source !== "paste") return
    onRawTextChange(raw, "markdown")
    if (debounceRef.current) window.clearTimeout(debounceRef.current)
    if (raw.trim() === "") {
      onChange(null)
      setPasteWarnings([])
      setPasteError(null)
      previewMut.reset()
      return
    }
    debounceRef.current = window.setTimeout(() => {
      previewMut.mutate(
        { kind: "skill", raw_text: raw, source_format: "markdown" },
        {
          onSuccess: (res) => {
            onChange(res.canonical_spec)
            setPasteWarnings(res.warnings ?? [])
            setPasteError(null)
            onSuggestedName(res.suggested_name ?? "")
            onSuggestedDescription(res.canonical_spec.skill?.description ?? "")
          },
          onError: (err) => {
            setPasteError(
              formatErr(err, t("capabilities.import.preview.errorFallback", "Failed to parse")),
            )
            setPasteWarnings([])
            onChange(null)
          },
        },
      )
    }, 350)
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [raw, source])

  /* ---- mode switch resets -------------------------------------------- */
  const onSourceChange = (next: SourceMode) => {
    if (next === source) return
    // Mode switch tears down the spec: paste vs zip produce different
    // canonical_spec shapes (files[]), can't share.
    setSource(next)
    onChange(null)
    previewMut.reset()
    presignMut.reset()
    if (next === "paste") {
      // Clear zip state so leftover ossKey doesn't reach commit
      setZipFile(null)
      setZipOssKey(null)
      setZipWarnings([])
      setZipError(null)
      onOssKeyChange(null)
      requestSeq.current++ // cancel in-flight zip chain
    } else {
      // Clear paste state — but keep the raw text so user can switch
      // back without retyping
      setPasteWarnings([])
      setPasteError(null)
      onRawTextChange("", "zip")
    }
  }

  /* ---- zip upload chain --------------------------------------------- */
  const acceptZip = async (picked: File) => {
    setZipError(null)
    if (!picked.name.toLowerCase().endsWith(".zip")) {
      setZipError(t("capabilities.import.skill.errors.notZip", "Please choose a .zip file"))
      return
    }
    if (picked.size > ZIP_MAX_BYTES) {
      setZipError(
        t("capabilities.import.skill.errors.tooLarge", "File exceeds 8 MiB — the server will reject it"),
      )
      return
    }
    const myReq = ++requestSeq.current
    setZipFile(picked)
    setZipOssKey(null)
    setZipWarnings([])
    onChange(null)
    onOssKeyChange(null)

    try {
      const presign = await presignMut.mutateAsync({
        filename: picked.name,
        prefix: "skill",
      })
      if (myReq !== requestSeq.current) return
      await putToPresignedURL(presign, picked)
      if (myReq !== requestSeq.current) return
      const preview = await previewMut.mutateAsync({
        kind: "skill",
        source_format: "zip",
        oss_key: presign.ossKey,
        upload_source: "zip",
      })
      if (myReq !== requestSeq.current) return
      setZipOssKey(presign.ossKey)
      setZipWarnings(preview.warnings ?? [])
      onOssKeyChange(presign.ossKey)
      if (preview.canonical_spec.kind === "skill" && preview.canonical_spec.skill) {
        onChange(preview.canonical_spec)
        if (preview.suggested_name) onSuggestedName(preview.suggested_name)
        onSuggestedDescription(preview.canonical_spec.skill.description ?? "")
      } else {
        onChange(null)
      }
    } catch (err) {
      if (myReq !== requestSeq.current) return
      setZipError(
        formatErr(err, t("capabilities.import.preview.errorFallback", "Failed to parse")),
      )
      onChange(null)
      onOssKeyChange(null)
    }
  }

  const clearZip = () => {
    requestSeq.current++
    setZipFile(null)
    setZipOssKey(null)
    setZipWarnings([])
    setZipError(null)
    onChange(null)
    onOssKeyChange(null)
    presignMut.reset()
    previewMut.reset()
  }

  /* ---- render --------------------------------------------------------- */
  const skill = value?.skill ?? null
  const busy = source === "zip" ? presignMut.isPending || previewMut.isPending : false

  const status: "idle" | "loading" | "error" | "ready" =
    source === "paste"
      ? previewMut.isPending
        ? "loading"
        : pasteError
          ? "error"
          : skill
            ? "ready"
            : "idle"
      : busy
        ? "loading"
        : zipError
          ? "error"
          : skill
            ? "ready"
            : "idle"

  const warnings = source === "paste" ? pasteWarnings : zipWarnings
  const errorMessage = source === "paste" ? pasteError : zipError

  return (
    <div className="grid gap-3">
      <Tabs value={source} onValueChange={(next) => onSourceChange(next as SourceMode)}>
        <TabsList aria-label={t("capabilities.import.skill.source.label", "Import method")}>
          <TabsTrigger value="paste">
            <ClipboardPaste className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
            {t("capabilities.import.skill.source.paste", "Paste Markdown")}
          </TabsTrigger>
          <TabsTrigger value="zip">
            <FileArchive className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
            {t("capabilities.import.skill.source.zip", "Upload zip")}
          </TabsTrigger>
        </TabsList>
      </Tabs>

      {/* Layout:
       *   paste mode → two columns (editor on the left, live preview on
       *     the right, side-by-side comparison is the point).
       *   zip mode  → single column. The dropzone is a small target that
       *     looks lonely in a half-width column, and the preview wants
       *     every pixel it can get (SKILL.md and supporting files all stack
       *     vertically). Stack input above preview instead. */}
      <div className={source === "paste" ? "grid gap-4 md:grid-cols-2" : "grid gap-4"}>
        <div className={`min-w-0 ${source === "paste" ? "max-w-3xl" : ""}`}>
          {source === "paste" ? (
            <>
              <Label htmlFor="import-skill-markdown">{t("capabilities.import.skill.markdown", "Markdown content")}</Label>
              <Textarea
                id="import-skill-markdown"
                value={raw}
                onChange={(e) => setRaw(e.target.value)}
                rows={20}
                placeholder={t(
                  "capabilities.import.skill.placeholder",
                  `---\nname: code-reviewer\ndescription: Review a diff and call out risky changes\n---\n\nYou are a careful code reviewer. When the user pastes a diff, walk through:\n\n1. Correctness — does the change do what it claims?\n2. Risk — what could break in production?\n3. Style — does it match the surrounding conventions?\n\nKeep responses concise.`,
                )}
                className="font-mono text-xs"
                spellCheck={false}
                autoCorrect="off"
                autoCapitalize="off"
              />
              <p className="mt-1 text-xs text-fg-muted">
                {t(
                  "capabilities.import.skill.pasteHelp",
                  "Supports Markdown with YAML frontmatter. name + description come from the frontmatter; the body is injected into the model as the instruction.",
                )}
              </p>
            </>
          ) : (
            <>
              <Label>{t("capabilities.import.skill.zipLabel", "Upload Skill zip")}</Label>
              <SkillZipDropzone
                file={zipFile}
                busy={busy}
                busyLabel={
                  presignMut.isPending
                    ? t("capabilities.import.skill.uploading", "Uploading…")
                    : previewMut.isPending
                      ? t("capabilities.import.skill.parsing", "Parsing…")
                      : undefined
                }
                onPick={(f) => void acceptZip(f)}
                onClear={clearZip}
                localError={zipError}
              />
              <p className="mt-1 text-xs text-fg-muted">
                {t(
                  "capabilities.import.skill.zipHelp",
                  "The zip must contain a SKILL.md at the root or one level deep. Any supporting files and directories are optional and imported alongside it.",
                )}
              </p>
            </>
          )}
        </div>

        {/* ---- PREVIEW: half-width beside the editor in paste mode, full
         *  width under the dropzone in zip mode. ---- */}
        <div className={`min-w-0 space-y-3 ${source === "paste" ? "max-w-3xl" : ""}`}>
          <ImportPreview
            status={status}
            errorMessage={errorMessage}
            warnings={warnings}
            suggestedName={status === "ready" ? skill?.slug : undefined}
            description={status === "ready" ? skill?.description : undefined}
            kind="skill"
          />

          {status === "ready" && skill && (
            (skill.files && skill.files.length > 0) ? (
              <SkillFileTree skill={skill} />
            ) : (
              <SinglePreview skill={skill} />
            )
          )}
        </div>
      </div>

      {/* Hidden ossKey passthrough — purely for the dev-tools view; the
          parent already receives ossKey via onOssKeyChange so this serves
          as a debug breadcrumb in the DOM. */}
      <input type="hidden" value={zipOssKey ?? ""} readOnly aria-hidden />
    </div>
  )
}

function SinglePreview({
  skill,
}: {
  skill: NonNullable<CanonicalSpec["skill"]>
}) {
  const { t } = useTranslation("admin")
  return (
    <section className="border-t border-line pt-3">
      <h4 className="text-sm font-medium text-fg">{skill.title || skill.slug}</h4>
      <code className="font-mono text-xs text-fg-muted">{skill.slug}</code>

      {/* description intentionally omitted — ImportPreview above already
       *  surfaces it on the "ready" line, repeating it here was noisy. */}

      {skill.trigger && (
        <div className="mt-3">
          <p className="mb-1 text-xs text-fg-muted">{t("capabilities.import.skill.trigger", "Trigger")}</p>
          <code className="block whitespace-pre-wrap rounded-md bg-surface-muted p-2 font-mono text-xs text-fg">
            {skill.trigger}
          </code>
        </div>
      )}

      <div className="mt-3">
        <p className="mb-1 text-xs text-fg-muted">
          {t("capabilities.import.skill.instruction", "Instruction (injected into the model)")}
        </p>
        <pre className="m-0 max-h-[280px] overflow-auto whitespace-pre-wrap rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
          {skill.instruction}
        </pre>
      </div>
    </section>
  )
}

function formatErr(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.envelope.message
  if (err instanceof Error) return err.message
  return fallback
}
