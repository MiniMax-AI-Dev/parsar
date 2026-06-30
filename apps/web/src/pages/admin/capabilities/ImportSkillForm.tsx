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
              formatErr(err, t("capabilities.import.preview.errorFallback", "解析失败")),
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
      setZipError(t("capabilities.import.skill.errors.notZip", "请选择 .zip 文件"))
      return
    }
    if (picked.size > ZIP_MAX_BYTES) {
      setZipError(
        t("capabilities.import.skill.errors.tooLarge", "文件超过 8 MiB,服务端会拒绝"),
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
      await putToPresignedURL(presign.uploadUrl, picked)
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
        formatErr(err, t("capabilities.import.preview.errorFallback", "解析失败")),
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
      <SourceModeSwitch value={source} onChange={onSourceChange} />

      {/* Layout:
       *   paste mode → two columns (editor on the left, live preview on
       *     the right, side-by-side comparison is the point).
       *   zip mode  → single column. The dropzone is a small target that
       *     looks lonely in a half-width column, and the preview wants
       *     every pixel it can get (SKILL.md source, references/, scripts/
       *     all stack vertically). Stack input above preview instead. */}
      <div
        className={
          source === "paste"
            ? "grid gap-4 md:grid-cols-2"
            : "grid gap-4"
        }
      >
        {/* In paste mode the editor stays narrow (max-w-3xl) so the
         *  side-by-side preview reads naturally. In zip mode the dropzone
         *  is the only thing here and it should match the dialog width —
         *  half-width looks unbalanced against the full-width preview
         *  below. min-w-0 keeps long unbroken copy from pushing the grid
         *  wider than the dialog. */}
        <div className={`min-w-0 space-y-2 ${source === "paste" ? "max-w-3xl" : ""}`}>
          {source === "paste" ? (
            <>
              <p className="text-sm font-medium text-fg-muted">
                {t("capabilities.import.skill.markdown", "Markdown 内容")}
              </p>
              <textarea
                value={raw}
                onChange={(e) => setRaw(e.target.value)}
                rows={20}
                placeholder={t(
                  "capabilities.import.skill.placeholder",
                  `---\nname: code-reviewer\ndescription: Review a diff and call out risky changes\n---\n\nYou are a careful code reviewer. When the user pastes a diff, walk through:\n\n1. Correctness — does the change do what it claims?\n2. Risk — what could break in production?\n3. Style — does it match the surrounding conventions?\n\nKeep responses concise.`,
                )}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs leading-relaxed shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300"
                spellCheck={false}
                autoCorrect="off"
                autoCapitalize="off"
              />
              <p className="text-xs text-fg-subtle">
                {t(
                  "capabilities.import.skill.pasteHelp",
                  "支持带 YAML frontmatter 的 Markdown。name + description 来自 frontmatter,正文作为 instruction 注入到模型。",
                )}
              </p>
            </>
          ) : (
            <>
              <p className="text-sm font-medium text-fg-muted">
                {t("capabilities.import.skill.zipLabel", "上传 Skill zip")}
              </p>
              <SkillZipDropzone
                file={zipFile}
                busy={busy}
                busyLabel={
                  presignMut.isPending
                    ? t("capabilities.import.skill.uploading", "上传中…")
                    : previewMut.isPending
                      ? t("capabilities.import.skill.parsing", "解析中…")
                      : undefined
                }
                onPick={(f) => void acceptZip(f)}
                onClear={clearZip}
                localError={zipError}
              />
              <p className="text-xs text-fg-subtle">
                {t(
                  "capabilities.import.skill.zipHelp",
                  "zip 内需包含 SKILL.md(可在根目录或一层子目录)。references/、scripts/ 等子目录会一并导入。",
                )}
              </p>
            </>
          )}
        </div>

        {/* ---- PREVIEW ----------------------------------
         *  In paste mode the parent grid keeps this at half-width so
         *  side-by-side comparison works. In zip mode the preview
         *  takes the full dialog width — the user uploaded a directory
         *  worth of files and wants to see every line, no point
         *  capping at 768px and wasting the right ~30% as whitespace.
         *  min-w-0 same reason as the input column above. */}
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
              <SinglePreviewCard skill={skill} />
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

function SourceModeSwitch({
  value,
  onChange,
}: {
  value: SourceMode
  onChange: (next: SourceMode) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div
      className="inline-flex items-center gap-1 self-start rounded-md border border-line bg-surface-subtle p-0.5"
      role="tablist"
      aria-label={t("capabilities.import.skill.source.label", "导入方式")}
    >
      <ModeButton
        active={value === "paste"}
        icon={<ClipboardPaste className="h-3.5 w-3.5" />}
        label={t("capabilities.import.skill.source.paste", "粘贴 Markdown")}
        onClick={() => onChange("paste")}
      />
      <ModeButton
        active={value === "zip"}
        icon={<FileArchive className="h-3.5 w-3.5" />}
        label={t("capabilities.import.skill.source.zip", "上传 zip")}
        onClick={() => onChange("zip")}
      />
    </div>
  )
}

function ModeButton({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-sm transition-colors ${
        active
          ? "bg-surface text-fg shadow-sm"
          : "text-fg-subtle hover:bg-surface-muted hover:text-fg-muted"
      }`}
    >
      {icon}
      {label}
    </button>
  )
}

function SinglePreviewCard({
  skill,
}: {
  skill: NonNullable<CanonicalSpec["skill"]>
}) {
  const { t } = useTranslation("admin")
  return (
    <section className="space-y-3 rounded-lg border border-line bg-surface p-3">
      <header className="border-b border-line-muted pb-2">
        <h4 className="text-sm font-semibold text-fg">
          {skill.title || skill.slug}
        </h4>
        <code className="font-mono text-xs text-fg-subtle">{skill.slug}</code>
      </header>

      {/* description intentionally omitted — ImportPreview above already
       *  surfaces it on the "ready" card, repeating it here was noisy. */}

      {skill.trigger && (
        <Field label={t("capabilities.import.skill.trigger", "触发条件")}>
          <code className="block whitespace-pre-wrap rounded bg-surface-subtle px-2 py-1.5 font-mono text-xs text-fg-muted">
            {skill.trigger}
          </code>
        </Field>
      )}

      <Field
        label={t("capabilities.import.skill.instruction", "Instruction(注入到模型)")}
      >
        <pre className="max-h-[280px] overflow-auto whitespace-pre-wrap rounded bg-surface-subtle px-2 py-1.5 font-mono text-xs leading-relaxed text-fg-muted">
          {skill.instruction}
        </pre>
      </Field>
    </section>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1">
      <span className="text-xs font-medium uppercase tracking-wide text-fg-subtle">
        {label}
      </span>
      {children}
    </div>
  )
}

function formatErr(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.envelope.message
  if (err instanceof Error) return err.message
  return fallback
}
